// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
)

// UsageReconciler reconciles CommittedResource.Status usage fields (AssignedInstances, UsedResources,
// LastUsageReconcileAt) by running the deterministic VM-to-CR assignment periodically and on
// relevant change events.
type UsageReconciler struct {
	client.Client
	Conf    UsageReconcilerConfig
	UsageDB UsageDBClient
	Monitor UsageReconcilerMonitor
}

func (r *UsageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()

	var cr v1alpha1.CommittedResource
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := ctrl.LoggerFrom(ctx).WithValues("component", "usage-reconciler", "committedResource", req.Name)

	// Only active commitments have assigned VMs. Clear stale usage status if present.
	if cr.Spec.State != v1alpha1.CommitmentStatusConfirmed && cr.Spec.State != v1alpha1.CommitmentStatusGuaranteed {
		log.Info("skipping: commitment state is not active", "state", cr.Spec.State)
		if len(cr.Status.AssignedInstances) > 0 || len(cr.Status.UsedResources) > 0 {
			old := cr.DeepCopy()
			cr.Status.AssignedInstances = nil
			cr.Status.UsedResources = nil
			cr.Status.LastUsageReconcileAt = nil
			cr.Status.UsageObservedGeneration = nil
			if err := r.Status().Patch(ctx, &cr, client.MergeFrom(old)); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}

	cooldown := r.Conf.CooldownInterval.Duration

	// Gate: wait until the CR controller has accepted the current generation.
	// The CR controller writes the Ready condition (with ObservedGeneration) only after
	// updating the AcceptedSpec. Running before that would read stale capacity.
	// We don't requeue here — the acceptedGenerationPredicate watch fires when the
	// condition is written, triggering a fresh reconcile at that point.
	readyCond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if readyCond == nil || readyCond.ObservedGeneration != cr.Generation || readyCond.Status != metav1.ConditionTrue {
		log.Info("skipping: Ready condition not yet accepted for current generation",
			"generation", cr.Generation,
			"readyCondFound", readyCond != nil,
		)
		return ctrl.Result{}, nil
	}

	// Bypass cooldown when the spec generation has advanced since the last usage reconcile.
	// This ensures spec changes (e.g. shrink) are reflected immediately rather than waiting
	// for the next cooldown interval — follows the Kubernetes observedGeneration pattern.
	generationAdvanced := cr.Status.UsageObservedGeneration == nil ||
		*cr.Status.UsageObservedGeneration != cr.Generation
	if !generationAdvanced && cr.Status.LastUsageReconcileAt != nil {
		if elapsed := time.Since(cr.Status.LastUsageReconcileAt.Time); elapsed < cooldown {
			return ctrl.Result{RequeueAfter: cooldown - elapsed}, nil
		}
	}

	log = log.WithValues("projectID", cr.Spec.ProjectID)
	trigger := "periodic"
	if generationAdvanced {
		trigger = "generation-change"
	}
	logger := log
	logger.Info("usage reconcile starting", "trigger", trigger, "generation", cr.Generation)

	calc := &UsageCalculator{client: r.Client, usageDB: r.UsageDB}

	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		r.Monitor.reconcileDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return ctrl.Result{}, err
	}

	commitmentsByAZFG, err := calc.buildCommitmentCapacityMap(ctx, logger, cr.Spec.ProjectID)
	if err != nil {
		r.Monitor.reconcileDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return ctrl.Result{}, err
	}
	if len(commitmentsByAZFG) == 0 {
		logger.Info("no active commitments found for project, retrying after cooldown")
		return ctrl.Result{RequeueAfter: cooldown}, nil
	}

	// Derive the known AZs from the commitment map so that NormalizeAZ maps VM AZ strings
	// to the same values used as commitment keys. VMs in unrecognised AZs get "unknown" and
	// are treated as PAYG, which is the correct fallback.
	allAZs := make([]liquid.AvailabilityZone, 0, len(commitmentsByAZFG))
	seenAZs := make(map[liquid.AvailabilityZone]struct{})
	for _, states := range commitmentsByAZFG {
		for _, state := range states {
			az := liquid.AvailabilityZone(state.AvailabilityZone)
			if _, ok := seenAZs[az]; !ok {
				seenAZs[az] = struct{}{}
				allAZs = append(allAZs, az)
			}
		}
	}

	vms, err := calc.getProjectVMs(ctx, logger, cr.Spec.ProjectID, flavorGroups, allAZs)
	if err != nil {
		r.Monitor.reconcileDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return ctrl.Result{}, err
	}
	sortVMsForUsageCalculation(vms)
	calc.assignVMsToCommitments(vms, commitmentsByAZFG)

	now := metav1.Now()
	written := 0
	totalAssigned := 0
	var writeErr error
	for _, group := range commitmentsByAZFG {
		for _, state := range group {
			if err := r.writeUsageStatus(ctx, state, now); err != nil {
				logger.Error(err, "failed to write usage status", "commitmentUUID", state.CommitmentUUID)
				writeErr = err
			} else {
				written++
				totalAssigned += len(state.AssignedInstances)
			}
		}
	}
	if writeErr != nil {
		return ctrl.Result{}, writeErr
	}

	r.Monitor.reconcileDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	// Observe status age once per reconcile, not once per commitment, to avoid biasing the
	// histogram toward projects with many commitments.
	if written > 0 && cr.Status.LastUsageReconcileAt != nil {
		r.Monitor.statusAge.Observe(now.Time.Sub(cr.Status.LastUsageReconcileAt.Time).Seconds())
	}
	r.Monitor.assignedInstances.WithLabelValues(cr.Spec.ProjectID).Set(float64(totalAssigned))

	logger.Info("usage reconcile complete",
		"commitments", written,
		"vms", len(vms),
		"assignedInstances", totalAssigned,
	)

	// Successful reconcile schedules the next run after the cooldown — acts as the periodic fallback.
	return ctrl.Result{RequeueAfter: cooldown}, nil
}

// writeUsageStatus patches AssignedInstances, UsedResources, and LastUsageReconcileAt on the CommittedResource
// identified by state.CommitmentUUID.
func (r *UsageReconciler) writeUsageStatus(ctx context.Context, state *CommitmentStateWithUsage, now metav1.Time) error {
	var crList v1alpha1.CommittedResourceList
	if err := r.List(ctx, &crList, client.MatchingFields{idxCommittedResourceByUUID: state.CommitmentUUID}); err != nil {
		return err
	}
	if len(crList.Items) == 0 {
		return nil
	}
	target := &crList.Items[0]
	old := target.DeepCopy()

	usedBytes := state.TotalMemoryBytes - state.RemainingMemoryBytes
	usedQty := resource.NewQuantity(usedBytes, resource.BinarySI)
	usedCores := resource.NewQuantity(state.UsedVCPUs, resource.DecimalSI)

	target.Status.AssignedInstances = state.AssignedInstances
	target.Status.UsedResources = map[string]resource.Quantity{
		"memory": *usedQty,
		"cpu":    *usedCores,
	}
	target.Status.LastUsageReconcileAt = &now
	target.Status.UsageObservedGeneration = &target.Generation

	return r.Status().Patch(ctx, target, client.MergeFrom(old))
}

// hypervisorToCommittedResources maps a Hypervisor change to the CommittedResources of affected projects.
// When a hypervisor's VM list changes, all CommittedResources for projects that have reservations
// on that host need their usage re-evaluated.
func (r *UsageReconciler) hypervisorToCommittedResources(ctx context.Context, obj client.Object) []reconcile.Request {
	hvName := obj.GetName()
	log := ctrl.LoggerFrom(ctx)

	var reservationList v1alpha1.ReservationList
	if err := r.List(ctx, &reservationList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		log.Error(err, "failed to list reservations for hypervisor event", "hypervisor", hvName)
		return nil
	}

	projectIDs := make(map[string]struct{})
	for _, res := range reservationList.Items {
		if res.Status.Host == hvName && res.Spec.CommittedResourceReservation != nil {
			projectIDs[res.Spec.CommittedResourceReservation.ProjectID] = struct{}{}
		}
	}
	if len(projectIDs) == 0 {
		return nil
	}

	var allCRs v1alpha1.CommittedResourceList
	if err := r.List(ctx, &allCRs); err != nil {
		log.Error(err, "failed to list CommittedResources for hypervisor event", "hypervisor", hvName)
		return nil
	}

	var requests []reconcile.Request
	for _, cr := range allCRs.Items {
		if _, affected := projectIDs[cr.Spec.ProjectID]; affected {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cr.Name},
			})
		}
	}
	return requests
}

// SetupWithManager registers the usage reconciler with the controller manager.
func (r *UsageReconciler) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	log := ctrl.Log.WithName("committed-resource-usage")
	log.Info("starting usage reconciler", "cooldownInterval", r.Conf.CooldownInterval.Duration)

	if err := indexCommittedResourceByUUID(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up committed resource field index: %w", err)
	}

	bldr := multicluster.BuildController(mcl, mgr)

	// Watch CommittedResource status updates where the CR controller has just accepted the
	// current generation. Fires when the Ready condition's ObservedGeneration advances to match
	// metadata.generation. We intentionally do NOT watch spec changes (GenerationChangedPredicate):
	// capacity is read from AcceptedSpec in status, which is only valid after the CR controller
	// has finished — so triggering on spec changes would always hit the readiness gate and do nothing.
	var err error
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResource{},
		&handler.EnqueueRequestForObject{},
		acceptedGenerationPredicate{log: log},
	)
	if err != nil {
		return err
	}

	// Watch Hypervisor CRDs: when VM instances on a host change, re-evaluate usage for
	// projects that have reservations on that host.
	bldr, err = bldr.WatchesMulticluster(
		&hv1.Hypervisor{},
		handler.EnqueueRequestsFromMapFunc(r.hypervisorToCommittedResources),
	)
	if err != nil {
		return err
	}

	// MaxConcurrentReconciles=1: the per-project assignment is globally consistent only when
	// run serially — concurrent runs for the same project could produce conflicting writes.
	return bldr.Named("committed-resource-usage").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

// acceptedGenerationPredicate fires on status-only updates where the CR controller has
// accepted the current spec generation (Ready=True, ObservedGeneration==metadata.generation)
// but the usage reconciler hasn't yet processed it (UsageObservedGeneration lags behind).
// Checking usage lag instead of Ready advancement makes it resilient to the race between
// the predicate firing and the reconciler reading from cache: if the first reconcile misses
// the window, the next status-only update re-fires the predicate.
type acceptedGenerationPredicate struct {
	predicate.Funcs
	log logr.Logger
}

func (p acceptedGenerationPredicate) Update(e event.UpdateEvent) bool {
	oldCR, ok1 := e.ObjectOld.(*v1alpha1.CommittedResource)
	newCR, ok2 := e.ObjectNew.(*v1alpha1.CommittedResource)
	if !ok1 || !ok2 {
		return false
	}
	// Only react to status-only updates; spec changes are handled by GenerationChangedPredicate.
	if oldCR.Generation != newCR.Generation {
		return false
	}
	newCond := meta.FindStatusCondition(newCR.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if newCond == nil || newCond.Status != metav1.ConditionTrue || newCond.ObservedGeneration != newCR.Generation {
		return false
	}
	// Don't fire if usage is already up to date for the accepted generation.
	if newCR.Status.UsageObservedGeneration != nil && *newCR.Status.UsageObservedGeneration >= newCond.ObservedGeneration {
		return false
	}
	p.log.Info("predicate fired: Ready accepted, usage not yet up to date",
		"name", newCR.Name, "generation", newCR.Generation,
		"usageObservedGeneration", newCR.Status.UsageObservedGeneration)
	return true
}

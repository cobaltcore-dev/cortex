// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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

	// Only active commitments have assigned VMs. Clear stale usage status if present.
	if cr.Spec.State != v1alpha1.CommitmentStatusConfirmed && cr.Spec.State != v1alpha1.CommitmentStatusGuaranteed {
		if len(cr.Status.AssignedInstances) > 0 || len(cr.Status.UsedResources) > 0 {
			old := cr.DeepCopy()
			cr.Status.AssignedInstances = nil
			cr.Status.UsedResources = nil
			cr.Status.LastUsageReconcileAt = nil
			if err := r.Status().Patch(ctx, &cr, client.MergeFrom(old)); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}

	cooldown := r.Conf.CooldownInterval.Duration
	if cr.Status.LastUsageReconcileAt != nil {
		if elapsed := time.Since(cr.Status.LastUsageReconcileAt.Time); elapsed < cooldown {
			return ctrl.Result{RequeueAfter: cooldown - elapsed}, nil
		}
	}

	logger := ctrl.LoggerFrom(ctx).WithValues(
		"component", "usage-reconciler",
		"committedResource", req.Name,
		"projectID", cr.Spec.ProjectID,
	)

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
				// Observe status age: how long ago was it last reconciled before this run.
				if cr.Status.LastUsageReconcileAt != nil {
					r.Monitor.statusAge.Observe(now.Time.Sub(cr.Status.LastUsageReconcileAt.Time).Seconds())
				}
			}
		}
	}
	if writeErr != nil {
		return ctrl.Result{}, writeErr
	}

	r.Monitor.reconcileDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
	r.Monitor.assignedInstances.Set(float64(totalAssigned))

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
	bldr := multicluster.BuildController(mcl, mgr)
	// Watch CommittedResource spec changes (generation change = spec changed; status-only
	// updates do not increment generation and are suppressed to avoid reconcile loops).
	var err error
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResource{},
		&handler.EnqueueRequestForObject{},
		predicate.GenerationChangedPredicate{},
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

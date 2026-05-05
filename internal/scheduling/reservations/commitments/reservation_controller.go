// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
)

// CommitmentReservationController reconciles commitment Reservation objects
type CommitmentReservationController struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf ReservationControllerConfig
	// SchedulerClient for making scheduler API calls.
	SchedulerClient *reservations.SchedulerClient
}

// echoParentGeneration copies Spec.CommittedResourceReservation.ParentGeneration to
// Status.CommittedResourceReservation.ObservedParentGeneration so the CommittedResource
// controller can confirm this reservation was processed for the current CR generation.
func echoParentGeneration(res *v1alpha1.Reservation) {
	if res.Spec.CommittedResourceReservation == nil {
		return
	}
	if res.Status.CommittedResourceReservation == nil {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
	}
	res.Status.CommittedResourceReservation.ObservedParentGeneration = res.Spec.CommittedResourceReservation.ParentGeneration
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Note: This controller only handles commitment reservations, as filtered by the predicate.
func (r *CommitmentReservationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the reservation object first to check for creator request ID.
	var res v1alpha1.Reservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Ignore not-found errors, since they can't be fixed by an immediate requeue
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Use creator request ID from annotation for end-to-end traceability if available,
	// otherwise generate a new one for this reconcile loop.
	if creatorReq := res.Annotations[v1alpha1.AnnotationCreatorRequestID]; creatorReq != "" {
		ctx = WithGlobalRequestID(ctx, creatorReq)
	} else {
		ctx = WithNewGlobalRequestID(ctx)
	}
	ctx = reservations.WithRequestID(ctx, req.Name)
	logger := LoggerFromContext(ctx).WithValues("component", "controller", "reservation", req.Name)

	// filter for CR reservations
	resourceName := ""
	if res.Spec.CommittedResourceReservation != nil {
		resourceName = res.Spec.CommittedResourceReservation.ResourceName
	}
	if resourceName == "" {
		logger.Info("reservation has no resource name, skipping")
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingResourceName",
			Message: "reservation has no resource name",
		})
		echoParentGeneration(&res)
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	if res.IsReady() {
		logger.V(1).Info("reservation is active, verifying allocations")

		// Sync ObservedParentGeneration if the CR controller bumped ParentGeneration since
		// the last time this reservation was processed (e.g. after a spec update). Without
		// this patch the CR controller would spin in Reserving forever for already-ready slots.
		if res.Spec.CommittedResourceReservation != nil &&
			(res.Status.CommittedResourceReservation == nil ||
				res.Status.CommittedResourceReservation.ObservedParentGeneration != res.Spec.CommittedResourceReservation.ParentGeneration) {
			old := res.DeepCopy()
			echoParentGeneration(&res)
			if err := r.Status().Patch(ctx, &res, client.MergeFrom(old)); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}

		// Verify all allocations in Spec against actual VM state
		result, err := r.reconcileAllocations(ctx, &res)
		if err != nil {
			logger.Error(err, "failed to reconcile allocations")
			return ctrl.Result{}, err
		}

		// Requeue with appropriate interval based on allocation state
		// Use shorter interval if there are allocations in grace period for faster verification
		if result.HasAllocationsInGracePeriod {
			return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalGracePeriod.Duration}, nil
		}
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalActive.Duration}, nil
	}

	// TODO trigger re-placement of unused reservations over time

	// Check if this is a pre-allocated reservation with allocations
	if res.Spec.CommittedResourceReservation != nil &&
		len(res.Spec.CommittedResourceReservation.Allocations) > 0 &&
		res.Spec.TargetHost != "" {
		// mark as ready without calling the placement API
		logger.Info("detected pre-allocated reservation",
			"targetHost", res.Spec.TargetHost,
			"allocatedVMs", len(res.Spec.CommittedResourceReservation.Allocations))

		old := res.DeepCopy()
		res.Status.Host = res.Spec.TargetHost
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "PreAllocated",
			Message: "reservation pre-allocated with VM allocations",
		})
		echoParentGeneration(&res)
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch pre-allocated reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}

		logger.Info("marked pre-allocated reservation as ready", "host", res.Status.Host)
		// Requeue immediately to run verification in next reconcile loop
		return ctrl.Result{Requeue: true}, nil
	}

	// Sync Spec values to Status fields for non-pre-allocated reservations
	// This ensures the observed state reflects the desired state from Spec
	// When TargetHost is set in Spec but not synced to Status, this means
	// the scheduler found a host and we need to mark the reservation as ready.
	if res.Spec.TargetHost != "" && res.Status.Host != res.Spec.TargetHost {
		old := res.DeepCopy()
		res.Status.Host = res.Spec.TargetHost
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ReservationActive",
			Message: "reservation is successfully scheduled",
		})
		echoParentGeneration(&res)
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to sync spec to status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		logger.Info("synced spec to status and marked ready", "host", res.Status.Host)
		// Return and let next reconcile handle allocation verification
		return ctrl.Result{}, nil
	}

	// Get project ID from CommittedResourceReservation spec if available.
	projectID := ""
	if res.Spec.CommittedResourceReservation != nil {
		projectID = res.Spec.CommittedResourceReservation.ProjectID
	}

	// Get AvailabilityZone from reservation if available
	availabilityZone := ""
	if res.Spec.AvailabilityZone != "" {
		availabilityZone = res.Spec.AvailabilityZone
	}

	// Get flavor details from flavor group knowledge CRD
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Info("flavor knowledge not ready, requeueing",
			"resourceName", resourceName,
			"error", err)
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry.Duration}, nil
	}

	// Search for the flavor across all flavor groups
	flavorGroupName, flavorDetails, err := reservations.FindFlavorInGroups(resourceName, flavorGroups)
	if err != nil {
		logger.Error(err, "flavor not found in any flavor group",
			"resourceName", resourceName)
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Get hypervisors from the cluster
	var hypervisorList hv1.HypervisorList
	if err := r.List(ctx, &hypervisorList); err != nil {
		logger.Error(err, "failed to list hypervisors")
		return ctrl.Result{}, err
	}

	// Build list of eligible hosts
	eligibleHosts := make([]schedulerdelegationapi.ExternalSchedulerHost, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		eligibleHosts = append(eligibleHosts, schedulerdelegationapi.ExternalSchedulerHost{
			ComputeHost: hv.Name,
		})
	}

	if len(eligibleHosts) == 0 {
		logger.Info("no hypervisors available for scheduling")
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NoHostsAvailable",
			Message: "no hypervisors available for scheduling",
		})
		echoParentGeneration(&res)
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry.Duration}, nil
	}

	// Select appropriate pipeline based on flavor group
	pipelineName := r.getPipelineForFlavorGroup(flavorGroupName, logger)
	logger.Info("selected pipeline for CR reservation",
		"flavorName", resourceName,
		"flavorGroup", flavorGroupName,
		"pipeline", pipelineName)

	// Use the SchedulerClient to schedule the reservation
	scheduleReq := reservations.ScheduleReservationRequest{
		InstanceUUID:     res.Name,
		ProjectID:        projectID,
		FlavorName:       flavorDetails.Name,
		FlavorExtraSpecs: flavorDetails.ExtraSpecs,
		MemoryMB:         flavorDetails.MemoryMB,
		VCPUs:            flavorDetails.VCPUs,
		EligibleHosts:    eligibleHosts,
		Pipeline:         pipelineName,
		AvailabilityZone: availabilityZone,
		// Set hint to indicate this is a CR reservation scheduling request.
		// This prevents other CR reservations from being unlocked during capacity filtering.
		SchedulerHints: map[string]any{
			"_nova_check_type": string(schedulerdelegationapi.ReserveForCommittedResourceIntent),
		},
	}

	scheduleResp, err := r.SchedulerClient.ScheduleReservation(ctx, scheduleReq)
	if err != nil {
		logger.Error(err, "failed to schedule reservation")
		return ctrl.Result{}, err
	}

	if len(scheduleResp.Hosts) == 0 {
		logger.Info("no hosts found for reservation", "reservation", res.Name, "flavorName", resourceName)
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NoHostsFound",
			Message: "no hosts found for reservation",
		})
		echoParentGeneration(&res)
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // No need to requeue, we didn't find a host.
	}

	// Update the reservation Spec with the found host (idx 0)
	// Only update Spec here - the Status will be synced in the next reconcile cycle
	// This avoids race conditions from doing two patches in one reconcile
	host := scheduleResp.Hosts[0]
	logger.Info("found host for reservation", "host", host)

	old := res.DeepCopy()
	res.Spec.TargetHost = host
	if err := r.Patch(ctx, &res, client.MergeFrom(old)); err != nil {
		// Ignore not-found errors during background deletion
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "failed to patch reservation spec")
			return ctrl.Result{}, err
		}
		// Object was deleted, no need to continue
		return ctrl.Result{}, nil
	}

	// The Spec patch will trigger a re-reconcile, which will sync Status in the
	// "Sync Spec values to Status" section above
	return ctrl.Result{}, nil
}

// reconcileAllocationsResult holds the outcome of allocation reconciliation.
type reconcileAllocationsResult struct {
	// HasAllocationsInGracePeriod is true if any allocations are still in grace period.
	HasAllocationsInGracePeriod bool
}

// reconcileAllocations verifies all allocations in Spec against actual VM state using the
// Hypervisor CRD as the sole source of truth.
//
// For new allocations (within grace period): the VM may not yet appear in the HV CRD
// (still spawning), so we skip verification and requeue with a short interval.
// For older allocations: we check the HV CRD; VMs not found are considered leaving and
// removed from the reservation.
func (r *CommitmentReservationController) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) (*reconcileAllocationsResult, error) {
	logger := LoggerFromContext(ctx).WithValues("component", "controller")
	result := &reconcileAllocationsResult{}
	now := time.Now()

	// Skip if no CommittedResourceReservation
	if res.Spec.CommittedResourceReservation == nil {
		return result, nil
	}

	// Skip if no allocations to verify
	if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
		logger.V(1).Info("no allocations to verify", "reservation", res.Name)
		return result, nil
	}

	expectedHost := res.Status.Host

	// Fetch the Hypervisor CRD for the expected host.
	var hypervisor hv1.Hypervisor
	hvInstanceSet := make(map[string]bool)
	if expectedHost != "" {
		if err := r.Get(ctx, client.ObjectKey{Name: expectedHost}, &hypervisor); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, fmt.Errorf("failed to get hypervisor %s: %w", expectedHost, err)
			}
			// Hypervisor not found — treat all post-grace-period VMs as stale.
			logger.Info("hypervisor CRD not found", "host", expectedHost)
		} else {
			// Build set of all VM UUIDs on this hypervisor for O(1) lookup.
			// Include both active and inactive VMs — stopped/shelved VMs still hold the slot.
			for _, inst := range hypervisor.Status.Instances {
				hvInstanceSet[inst.ID] = true
			}
			logger.V(1).Info("fetched hypervisor instances", "host", expectedHost, "instanceCount", len(hvInstanceSet))
		}
	}

	// Initialize status
	if res.Status.CommittedResourceReservation == nil {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
	}

	// Build new Status.Allocations map based on HV CRD state.
	newStatusAllocations := make(map[string]string)
	// Track allocations to remove from Spec (stale/leaving VMs).
	var allocationsToRemove []string

	for vmUUID, allocation := range res.Spec.CommittedResourceReservation.Allocations {
		allocationAge := now.Sub(allocation.CreationTimestamp.Time)
		isInGracePeriod := allocationAge < r.Conf.AllocationGracePeriod.Duration

		if isInGracePeriod {
			// New allocation: VM may not yet appear in the HV CRD (still spawning).
			// Signal to requeue with the short grace-period interval; skip verification.
			result.HasAllocationsInGracePeriod = true
			logger.V(1).Info("allocation in grace period, deferring verification",
				"vm", vmUUID,
				"allocationAge", allocationAge)
			continue
		}

		// Post-grace-period: use HV CRD as authoritative source.
		if hvInstanceSet[vmUUID] {
			newStatusAllocations[vmUUID] = expectedHost
			logger.V(1).Info("verified VM allocation via Hypervisor CRD",
				"vm", vmUUID,
				"host", expectedHost)
		} else {
			allocationsToRemove = append(allocationsToRemove, vmUUID)
			logger.Info("removing stale allocation (VM not found on hypervisor)",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost,
				"allocationAge", allocationAge,
				"gracePeriod", r.Conf.AllocationGracePeriod.Duration)
		}
	}

	// Patch the reservation
	old := res.DeepCopy()
	specChanged := false

	// Remove stale allocations from Spec
	if len(allocationsToRemove) > 0 {
		for _, vmUUID := range allocationsToRemove {
			delete(res.Spec.CommittedResourceReservation.Allocations, vmUUID)
		}
		specChanged = true
	}

	// Update Status.Allocations
	res.Status.CommittedResourceReservation.Allocations = newStatusAllocations

	// Patch Spec if changed (stale allocations removed)
	if specChanged {
		if err := r.Patch(ctx, res, client.MergeFrom(old)); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return result, nil
			}
			return nil, fmt.Errorf("failed to patch reservation spec: %w", err)
		}
		// Re-fetch to get the updated resource version for status patch
		if err := r.Get(ctx, client.ObjectKeyFromObject(res), res); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return result, nil
			}
			return nil, fmt.Errorf("failed to re-fetch reservation: %w", err)
		}
		// Capture the re-fetched state as the patch base BEFORE re-applying
		// the status update. Otherwise MergeFrom(old) would see no diff
		// and the status patch would be a no-op.
		old = res.DeepCopy()
		// Re-apply the status update that was overwritten by the re-fetch.
		res.Status.CommittedResourceReservation.Allocations = newStatusAllocations
	}

	// Patch Status
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, res, patch); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return result, nil
		}
		return nil, fmt.Errorf("failed to patch reservation status: %w", err)
	}

	logger.V(1).Info("reconciled allocations",
		"specAllocations", len(res.Spec.CommittedResourceReservation.Allocations),
		"statusAllocations", len(newStatusAllocations),
		"removedAllocations", len(allocationsToRemove),
		"hasAllocationsInGracePeriod", result.HasAllocationsInGracePeriod)

	return result, nil
}

// getPipelineForFlavorGroup returns the pipeline name for a given flavor group.
func (r *CommitmentReservationController) getPipelineForFlavorGroup(flavorGroupName string, logger logr.Logger) string {
	// Try exact match first (e.g., "2152" -> "kvm-cr-hana")
	if pipeline, ok := r.Conf.FlavorGroupPipelines[flavorGroupName]; ok {
		return pipeline
	}

	// Try wildcard fallback
	if pipeline, ok := r.Conf.FlavorGroupPipelines["*"]; ok {
		return pipeline
	}

	logger.Info("no pipeline configured for flavor group, using default", "flavorGroup", flavorGroupName, "defaultPipeline", r.Conf.PipelineDefault)
	return r.Conf.PipelineDefault
}

// hypervisorToReservations maps a Hypervisor change event to the set of CR reservations
// assigned to that host. Used as the event handler for the Hypervisor CRD watch so that
// when the hypervisor operator updates Status.Instances, affected reservations are
// immediately enqueued for reconciliation.
func (r *CommitmentReservationController) hypervisorToReservations(ctx context.Context, obj client.Object) []reconcile.Request {
	hvName := obj.GetName()
	var reservationList v1alpha1.ReservationList
	if err := r.List(ctx, &reservationList); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list reservations for hypervisor", "hypervisor", hvName)
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for _, res := range reservationList.Items {
		if res.Spec.Type != v1alpha1.ReservationTypeCommittedResource {
			continue
		}
		if res.Status.Host != hvName {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: res.Name, Namespace: res.Namespace},
		})
	}
	return requests
}

// Init initializes the reconciler with required clients and DB connection.
func (r *CommitmentReservationController) Init(ctx context.Context, conf ReservationControllerConfig) error {
	r.SchedulerClient = reservations.NewSchedulerClient(conf.SchedulerURL)
	logf.FromContext(ctx).Info("scheduler client initialized for commitment reservation controller", "url", conf.SchedulerURL)
	return nil
}

// commitmentReservationPredicate filters to only watch commitment reservations.
// This controller explicitly handles only commitment reservations (CR reservations),
// while failover reservations are handled by the separate failover controller.
var commitmentReservationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		res, ok := e.ObjectNew.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	GenericFunc: func(e event.GenericEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitmentReservationController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if err := r.Init(ctx, r.Conf); err != nil {
			return err
		}
		return nil
	})); err != nil {
		return err
	}

	// Use WatchesMulticluster to watch Reservations across all configured clusters
	// (home + remotes). This is required because Reservation CRDs may be stored
	// in remote clusters, not just the home cluster. Without this, the controller
	// would only see reservations in the home cluster's cache.
	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(
		&v1alpha1.Reservation{},
		&handler.EnqueueRequestForObject{},
		commitmentReservationPredicate,
	)
	if err != nil {
		return err
	}

	// Watch Hypervisor CRDs reactively: when the hypervisor operator updates
	// Status.Instances (VM appeared or disappeared), enqueue all reservations
	// assigned to that host. This replaces periodic polling for the established-VM
	// verification path — changes are detected in seconds rather than up to
	// RequeueIntervalActive. RequeueIntervalActive remains as a safety-net fallback.
	bldr, err = bldr.WatchesMulticluster(
		&hv1.Hypervisor{},
		handler.EnqueueRequestsFromMapFunc(r.hypervisorToReservations),
	)
	if err != nil {
		return err
	}

	return bldr.Named("commitment-reservation").
		WithOptions(controller.Options{
			// MaxConcurrentReconciles=1: conservative default. Note that this does NOT prevent
			// the cache-staleness race where two back-to-back reconciles both pick the same host
			// before the first write is visible to the capacity filter — that requires pessimistic
			// blocking at the scheduler level.
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

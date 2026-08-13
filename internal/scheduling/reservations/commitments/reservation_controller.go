// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
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

	"net/http"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud/v2"
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
	// DomainResolver resolves OpenStack domain IDs to domain names so the
	// domain_name scheduler hint can be populated for filter_external_customer.
	// Nil when KeystoneSecretRef is not configured; hint is omitted in that case.
	DomainResolver DomainResolver
	// Monitor reports over-subscription violations as Prometheus metrics.
	// Nil disables over-subscription detection entirely.
	Monitor *ReservationControllerMonitor

	// oversubscription tracking — mu protects the three maps below
	oversubscriptionMu            sync.Mutex
	oversubscriptionLastCheckedAt map[string]time.Time
	oversubscriptionPendingCheck  map[string]bool
	oversubscriptionFirstSeen     map[string]time.Time
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
	logger := LoggerFromContext(ctx).WithValues("reservation", req.Name)

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
		// Spec.TargetHost was cleared (e.g. oversubscription eviction) — revoke status so the
		// slot re-enters the placement flow on the next reconcile.
		if res.Spec.TargetHost == "" {
			old := res.DeepCopy()
			res.Status.Host = ""
			if res.Status.CommittedResourceReservation != nil {
				res.Status.CommittedResourceReservation.Allocations = nil
			}
			meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.ReservationConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "PlacementRevoked",
				Message: "target host was cleared; pending re-placement",
			})
			if err := r.Status().Patch(ctx, &res, client.MergeFrom(old)); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			logger.Info("revoked ready status after placement eviction, slot re-enters placement flow",
				"component", "oversubscription-check")
			return ctrl.Result{}, nil
		}

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
		// Check over-subscription after allocation verification (HV watch path).
		if requeueAfter := r.runOversubscriptionCheck(ctx, res.Status.Host); requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
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
		// Check over-subscription now that this slot is placed and Ready.
		if requeueAfter := r.runOversubscriptionCheck(ctx, res.Status.Host); requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
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

	// Resolve domain name for the domain_name scheduler hint consumed by
	// filter_external_customer to enforce host restrictions for external customers.
	// Resolution is best-effort: if the DomainResolver is not configured (no
	// KeystoneSecretRef) or the lookup fails, we log and proceed without the hint.
	// filter_external_customer already handles a missing hint by skipping the filter,
	// so omitting it degrades gracefully rather than blocking scheduling.
	schedulerHints := map[string]any{
		"_nova_check_type": string(schedulerdelegationapi.ReserveForCommittedResourceIntent),
	}
	if r.DomainResolver != nil && res.Spec.CommittedResourceReservation != nil && res.Spec.CommittedResourceReservation.DomainID != "" {
		domainName, err := r.DomainResolver.ResolveDomainName(ctx, res.Spec.CommittedResourceReservation.DomainID)
		if err != nil {
			logger.Error(err, "failed to resolve domain name for scheduler hint, proceeding without it",
				"domainID", res.Spec.CommittedResourceReservation.DomainID)
		} else {
			schedulerHints["domain_name"] = domainName
		}
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
		"pipeline", pipelineName,
		"reason", func() string {
			cond := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
			if cond != nil {
				return cond.Reason
			}
			return "initial"
		}())

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
		SchedulerHints:   schedulerHints,
	}
	scheduleOpts := scheduling.Options{
		ReadOnly:                      false, // mutates state (reservation placement)
		LockReservations:              true,  // don't unlock CR reservations; finding a slot, not placing a VM
		AssumeEmptyHosts:              false,
		IgnoredReservationTypes:       nil,
		MaxCandidates:                 1,
		SkipHistory:                   true,
		SkipInflight:                  false, // TODO pessimistic blocking needed, will be addressed in follow up ticket
		SkipCommittedResourceTracking: true,  // CR slot scheduling, not a VM placement
	}

	scheduleResp, err := r.SchedulerClient.ScheduleReservation(ctx, scheduleReq, scheduleOpts)
	if err != nil {
		logger.Error(err, "failed to schedule reservation")
		return ctrl.Result{}, err
	}

	if len(scheduleResp.Hosts) == 0 {
		logger.Info("no hosts found for reservation, will retry", "reservation", res.Name, "flavorName", resourceName)
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
		return ctrl.Result{}, fmt.Errorf("no hosts found for reservation %s (flavor %s)", res.Name, resourceName)
	}

	// Update the reservation Spec with the found host (idx 0)
	// Only update Spec here - the Status will be synced in the next reconcile cycle
	// This avoids race conditions from doing two patches in one reconcile
	host := scheduleResp.Hosts[0]
	logger.Info("found host for reservation", "host", host, "flavorName", resourceName)

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
// New allocations within the grace period are skipped — the VM may not yet appear in the
// HV CRD while it is still spawning. Older allocations are verified; VMs no longer present
// on their expected host are handled as follows:
//
// Live migration: when a confirmed VM is found on a different host, the reservation follows
// it only when the reservation has exactly one allocated VM and the new host has capacity.
// In all other cases (multiple VMs, or new host at capacity), the migrated VM is removed
// from the reservation so the slot remains available for re-use on the original host.
// Moving TargetHost when other VMs are present would cause those remaining VMs to appear
// misplaced on the next reconcile cycle.
func (r *CommitmentReservationController) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) (*reconcileAllocationsResult, error) {
	logger := LoggerFromContext(ctx)
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

	// Snapshot existing Status.Allocations before we overwrite it so we can detect
	// which VM UUIDs are newly confirmed after the patch.
	existingStatusAllocations := make(map[string]string, len(res.Status.CommittedResourceReservation.Allocations))
	for k, v := range res.Status.CommittedResourceReservation.Allocations {
		existingStatusAllocations[k] = v
	}

	// allHVs and allReservations are fetched lazily — only needed when a confirmed VM is
	// missing from its expected host and we need to scan for a live migration.
	var allHVs *hv1.HypervisorList
	var allReservations *v1alpha1.ReservationList

	ensureHVsAndReservations := func() error {
		if allHVs != nil && allReservations != nil {
			return nil
		}
		hvs := &hv1.HypervisorList{}
		if err := r.List(ctx, hvs); err != nil {
			return fmt.Errorf("failed to list hypervisors: %w", err)
		}
		res := &v1alpha1.ReservationList{}
		if err := r.List(ctx, res); err != nil {
			return fmt.Errorf("failed to list reservations: %w", err)
		}
		allHVs = hvs
		allReservations = res
		return nil
	}

	// Build new Status.Allocations map based on HV CRD state.
	newStatusAllocations := make(map[string]string)
	// Track allocations to remove from Spec (stale/leaving VMs).
	var allocationsToRemove []string

	// migrationTargetHost is set when the reservation has exactly one VM, that VM
	// live-migrated to a new host, and the new host has capacity.
	migrationTargetHost := ""

	for vmUUID, allocation := range res.Spec.CommittedResourceReservation.Allocations {
		allocationAge := now.Sub(allocation.CreationTimestamp.Time)
		isInGracePeriod := allocationAge < r.Conf.AllocationGracePeriod.Duration

		// Confirmed VMs (already in Status.Allocations) bypass the grace period:
		// their departure from the HV CRD is authoritative and must be acted on immediately.
		// Unconfirmed VMs still within the grace period may not yet appear in the HV CRD
		// (still spawning), so defer verification and requeue with a short interval.
		_, isConfirmed := existingStatusAllocations[vmUUID]
		if !isConfirmed && isInGracePeriod {
			result.HasAllocationsInGracePeriod = true
			logger.V(1).Info("allocation in grace period, deferring verification",
				"vm", vmUUID,
				"allocationAge", allocationAge)
			continue
		}

		// Post-grace-period or confirmed VM: use HV CRD as authoritative source.
		if hvInstanceSet[vmUUID] {
			newStatusAllocations[vmUUID] = expectedHost
			logger.V(1).Info("verified VM allocation via Hypervisor CRD",
				"vm", vmUUID,
				"host", expectedHost)
			continue
		}

		// VM not on the expected host. For unconfirmed post-grace VMs this is a clean
		// stale allocation — remove it without further searching.
		if !isConfirmed {
			allocationsToRemove = append(allocationsToRemove, vmUUID)
			logger.Info("removing stale allocation (VM not found on hypervisor)",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost,
				"allocationAge", allocationAge,
				"gracePeriod", r.Conf.AllocationGracePeriod.Duration)
			continue
		}

		// Confirmed VM missing from expected host — could be a live migration.
		// Scan all HVs lazily; the list is shared across any further misses this cycle.
		if err := ensureHVsAndReservations(); err != nil {
			return nil, err
		}

		var foundHost string
		var foundHV hv1.Hypervisor
		for _, hv := range allHVs.Items {
			if hv.Name == expectedHost {
				continue // already checked via hvInstanceSet above
			}
			for _, inst := range hv.Status.Instances {
				if inst.ID == vmUUID {
					foundHost = hv.Name
					foundHV = hv
					break
				}
			}
			if foundHost != "" {
				break
			}
		}

		if foundHost == "" {
			// VM is not on any known hypervisor. This covers two cases:
			// 1. The VM was terminated or evacuated — correct to remove.
			// 2. The VM is mid-live-migration: it has left host-old's HV CRD but
			//    host-new's CRD has not been updated yet. In this window the VM
			//    is incorrectly treated as gone and removed from the reservation.
			//    A VM CRD with lifecycle state (migrating/active) would close this
			//    gap; without one we accept this narrow race as a known limitation.
			allocationsToRemove = append(allocationsToRemove, vmUUID)
			logger.Info("removing confirmed allocation (VM not found on any hypervisor)",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost)
			continue
		}

		// VM found on a different host — live migration detected.
		//
		// Follow the VM only when this is the sole VM in the reservation and the new
		// host has capacity. Moving TargetHost with multiple VMs present would cause
		// the remaining VMs to appear misplaced on the next reconcile. When there are
		// multiple VMs, or the new host is at capacity, remove this VM so the slot
		// on the original host remains available for re-use.
		isSingleVM := len(res.Spec.CommittedResourceReservation.Allocations) == 1
		if isSingleVM && reservations.HostHasCapacityForReservation(allReservations.Items, foundHV, res) {
			logger.Info("VM live-migrated to host with capacity, updating TargetHost",
				"vm", vmUUID,
				"reservation", res.Name,
				"oldHost", expectedHost,
				"newHost", foundHost)
			migrationTargetHost = foundHost
			newStatusAllocations[vmUUID] = foundHost
		} else {
			logger.Info("removing VM from reservation after live migration: either multiple VMs present or new host lacks capacity",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost,
				"actualHost", foundHost,
				"singleVM", isSingleVM)
			allocationsToRemove = append(allocationsToRemove, vmUUID)
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

	// Advance both TargetHost and Status.Host in the same patch cycle to avoid a
	// transient state where Status.Host lags behind TargetHost and blocks capacity
	// accounting on the old host during the next reconcile.
	if migrationTargetHost != "" {
		res.Spec.TargetHost = migrationTargetHost
		res.Status.Host = migrationTargetHost
		specChanged = true
	}

	// Update Status.Allocations
	res.Status.CommittedResourceReservation.Allocations = newStatusAllocations

	// Patch Spec if changed (stale allocations removed and/or TargetHost updated)
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
		// Re-apply status updates that were overwritten by the re-fetch.
		res.Status.CommittedResourceReservation.Allocations = newStatusAllocations
		if migrationTargetHost != "" {
			res.Status.Host = migrationTargetHost
		}
	}

	// Proactively remove this VM UUID from all other candidate reservations that still
	// carry it in their Spec.Allocations. Only do this for VMs that are newly confirmed
	// in this reconcile cycle (present in newStatusAllocations but absent in the snapshot
	// taken before any patch) to avoid redundant work on subsequent reconciles.
	for vmUUID := range newStatusAllocations {
		if _, wasAlreadyConfirmed := existingStatusAllocations[vmUUID]; wasAlreadyConfirmed {
			continue
		}
		if err := r.cleanupCandidateReservations(ctx, res.Name, vmUUID); err != nil {
			return nil, fmt.Errorf("failed to cleanup candidate reservations for vm %s: %w", vmUUID, err)
		}
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

// cleanupCandidateReservations removes vmUUID from Spec.Allocations of all Reservation CRDs
// other than the one that just confirmed the VM. This is called once per newly confirmed VM
// so that candidate slots on non-selected hosts are freed immediately rather than waiting
// for those reservations' own grace period or periodic requeue.
func (r *CommitmentReservationController) cleanupCandidateReservations(ctx context.Context, confirmedReservationName, vmUUID string) error {
	logger := LoggerFromContext(ctx).WithValues("component", "controller", "vm", vmUUID)

	var candidates v1alpha1.ReservationList
	if err := r.List(ctx, &candidates, client.MatchingFields{idxReservationByAllocationVMUUID: vmUUID}); err != nil {
		return fmt.Errorf("failed to list candidate reservations: %w", err)
	}

	for i := range candidates.Items {
		candidate := &candidates.Items[i]
		if candidate.Name == confirmedReservationName {
			continue
		}
		if candidate.Spec.CommittedResourceReservation == nil {
			continue
		}
		if _, ok := candidate.Spec.CommittedResourceReservation.Allocations[vmUUID]; !ok {
			continue
		}
		old := candidate.DeepCopy()
		delete(candidate.Spec.CommittedResourceReservation.Allocations, vmUUID)
		if err := r.Patch(ctx, candidate, client.MergeFrom(old)); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to patch candidate reservation %s: %w", candidate.Name, err)
			}
		}
		logger.Info("removed vm from candidate reservation", "reservation", candidate.Name, "host", candidate.Status.Host)
	}
	return nil
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
	if err := r.List(ctx, &reservationList, client.MatchingFields{reservations.IdxReservationByHost: hvName}); err != nil {
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

	if conf.KeystoneSecretRef.Name != "" {
		var authenticatedHTTP *http.Client
		if conf.SSOSecretRef != nil {
			var err error
			authenticatedHTTP, err = sso.Connector{Client: r.Client}.FromSecretRef(ctx, *conf.SSOSecretRef)
			if err != nil {
				return fmt.Errorf("failed to initialize SSO client for domain resolver: %w", err)
			}
		}
		keystoneClient, err := keystone.Connector{Client: r.Client, HTTPClient: authenticatedHTTP}.FromSecretRef(ctx, conf.KeystoneSecretRef)
		if err != nil {
			return fmt.Errorf("failed to initialize keystone client for domain resolver: %w", err)
		}
		provider := keystoneClient.Client()
		identityURL, err := provider.EndpointLocator(gophercloud.EndpointOpts{
			Type:         "identity",
			Availability: gophercloud.Availability(keystoneClient.Availability()),
		})
		if err != nil {
			return fmt.Errorf("failed to locate keystone identity endpoint for domain resolver: %w", err)
		}
		sc := &gophercloud.ServiceClient{
			ProviderClient: provider,
			Endpoint:       identityURL,
			Type:           "identity",
		}
		r.DomainResolver = newKeystoneDomainResolver(sc)
		logf.FromContext(ctx).Info("domain resolver initialized for commitment reservation controller")
	} else {
		logf.FromContext(ctx).Info("keystoneSecretRef not configured — domain_name scheduler hint will not be set for CR reservations")
	}

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

// hvCapacityChangePredicate fires when Status.Instances, Status.Allocation,
// Status.EffectiveCapacity, or Status.Capacity changes on a Hypervisor. Instances covers
// VM presence (used by allocation verification); Allocation, EffectiveCapacity, and Capacity
// cover capacity accounting (used by the over-subscription check; Capacity is the fallback
// when EffectiveCapacity is nil).
var hvCapacityChangePredicate = predicate.Funcs{
	CreateFunc:  func(e event.CreateEvent) bool { _, ok := e.Object.(*hv1.Hypervisor); return ok },
	DeleteFunc:  func(e event.DeleteEvent) bool { _, ok := e.Object.(*hv1.Hypervisor); return ok },
	GenericFunc: func(e event.GenericEvent) bool { _, ok := e.Object.(*hv1.Hypervisor); return ok },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldHV, ok1 := e.ObjectOld.(*hv1.Hypervisor)
		newHV, ok2 := e.ObjectNew.(*hv1.Hypervisor)
		if !ok1 || !ok2 {
			return false
		}
		return !reflect.DeepEqual(oldHV.Status.Instances, newHV.Status.Instances) ||
			!reflect.DeepEqual(oldHV.Status.Allocation, newHV.Status.Allocation) ||
			!reflect.DeepEqual(oldHV.Status.EffectiveCapacity, newHV.Status.EffectiveCapacity) ||
			!reflect.DeepEqual(oldHV.Status.Capacity, newHV.Status.Capacity)
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

	if err := indexReservationByAllocationVMUUID(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up reservation allocation VM UUID index: %w", err)
	}
	if err := reservations.IndexReservationByHost(context.Background(), mcl); err != nil {
		return fmt.Errorf("failed to set up reservation by host index: %w", err)
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
		hvCapacityChangePredicate,
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

// runOversubscriptionCheck detects host over-subscription and drives remediation.
// Rate-limited per host: skipped checks mark the host pending so the next reconcile retries.
// Returns non-zero when the caller should requeue (grace period pending or after eviction).
func (r *CommitmentReservationController) runOversubscriptionCheck(ctx context.Context, host string) time.Duration {
	if host == "" || r.Monitor == nil || !r.Conf.EnableOversubscriptionCheck {
		return 0
	}

	r.oversubscriptionMu.Lock()
	defer r.oversubscriptionMu.Unlock()

	logger := LoggerFromContext(ctx).WithValues("component", "oversubscription-check", "host", host)

	gracePeriod := r.Conf.OversubscriptionGracePeriod.Duration
	if gracePeriod == 0 {
		gracePeriod = 2 * time.Minute
	}
	minCheckInterval := r.Conf.OversubscriptionMinCheckInterval.Duration
	if minCheckInterval == 0 {
		minCheckInterval = 30 * time.Second
	}

	if r.oversubscriptionLastCheckedAt == nil {
		r.oversubscriptionLastCheckedAt = make(map[string]time.Time)
		r.oversubscriptionPendingCheck = make(map[string]bool)
		r.oversubscriptionFirstSeen = make(map[string]time.Time)
	}

	// Rate limit: if checked recently and if pending flag marks already dirty
	if timeSinceLastCheck := time.Since(r.oversubscriptionLastCheckedAt[host]); timeSinceLastCheck < minCheckInterval {
		if !r.oversubscriptionPendingCheck[host] {
			r.oversubscriptionPendingCheck[host] = true
			requeue := minCheckInterval - timeSinceLastCheck + time.Second
			logger.V(1).Info("over-subscription check rate-limited, requeue scheduled",
				"lastCheckedAgo", timeSinceLastCheck.Round(time.Second),
				"requeueIn", requeue.Round(time.Second))
			return requeue
		}
		// already dirty, so someone else requeued already
		return 0
	}

	var hv hv1.Hypervisor
	if err := r.Get(ctx, client.ObjectKey{Name: host}, &hv); err != nil {
		logger.Error(err, "failed to get hypervisor for over-subscription check")
		return 0
	}
	var hostReservations v1alpha1.ReservationList
	if err := r.List(ctx, &hostReservations, client.MatchingFields{reservations.IdxReservationByHost: host}); err != nil {
		logger.Error(err, "failed to list reservations for over-subscription check")
		return 0
	}

	logger.Info("running over-subscription check",
		"slotCount", len(hostReservations.Items),
		"lastCheckedAgo", time.Since(r.oversubscriptionLastCheckedAt[host]).Round(time.Second),
		"violationFirstSeen", func() string {
			if t := r.oversubscriptionFirstSeen[host]; !t.IsZero() {
				return time.Since(t).Round(time.Second).String()
			}
			return "none"
		}())

	// Mark check as done and clear dirty flag before delegating.
	r.oversubscriptionLastCheckedAt[host] = time.Now()
	r.oversubscriptionPendingCheck[host] = false
	firstSeen := r.oversubscriptionFirstSeen[host]

	evicted, resolved, err := r.checkHostOversubscription(ctx, host, hostReservations.Items, hv, r.Monitor, gracePeriod, firstSeen)
	if err != nil {
		logger.Error(err, "over-subscription check failed")
		return 0
	}

	// No violation — clear grace period state.
	if resolved {
		if !firstSeen.IsZero() {
			logger.Info("host over-subscription resolved",
				"violationDuration", time.Since(firstSeen).Round(time.Second))
		} else {
			logger.Info("host over-subscription check: no violations detected")
		}
		delete(r.oversubscriptionFirstSeen, host)
		return 0
	}

	// Slot evicted — reset grace period so next eviction waits a full interval.
	if evicted {
		r.oversubscriptionFirstSeen[host] = time.Now()
		logger.Info("slot evicted, next remediation check in", "nextCheckIn", gracePeriod)
		return gracePeriod
	}

	// Violation detected for the first time — start grace period, requeue after it.
	if firstSeen.IsZero() {
		r.oversubscriptionFirstSeen[host] = time.Now()
		logger.Info("violation detected first time, starting grace period", "gracePeriod", gracePeriod)
		return gracePeriod
	}

	// Grace period still running — requeue with remaining time.
	if elapsed := time.Since(firstSeen); elapsed < gracePeriod {
		var remaining = gracePeriod - elapsed
		logger.Info("violation still present, grace period running", "elapsed", elapsed.Round(time.Second), "remaining", remaining.Round(time.Second))
		return remaining
	}

	// Grace period elapsed but checkHostOversubscription did not evict (no candidates).
	return 0
}

// unusedRatioBuckets returns unused/total bucketed into 10 steps (0–10); 0 when total is zero.
func unusedRatioBuckets(unused, total resource.Quantity) int64 {
	if t := total.Value(); t != 0 {
		return unused.Value() * 10 / t
	}
	return 0
}

// formatQuantityMap converts a resource quantity map to a string map for logging.
func formatQuantityMap[K ~string](m map[K]resource.Quantity) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v.String()
	}
	return out
}

// computeViolations returns the per-resource excess (positive quantity) for any resource where
// the host is over-subscribed. Returns nil if capacity data is unavailable, empty map if no violation.
func computeViolations(allReservations []v1alpha1.Reservation, hv hv1.Hypervisor) map[hv1.ResourceName]resource.Quantity {
	free := reservations.HostFreeCapacity(allReservations, hv)
	if free == nil {
		return nil
	}
	zero := resource.MustParse("0")
	violations := make(map[hv1.ResourceName]resource.Quantity)
	for rn, f := range free {
		if f.Cmp(zero) < 0 {
			excess := f.DeepCopy()
			excess.Neg()
			violations[rn] = excess
		}
	}
	return violations
}

// selectEvictionTarget picks the best slot to evict from allReservations given the active violations.
func (r *CommitmentReservationController) selectEvictionTarget(
	ctx context.Context,
	allReservations []v1alpha1.Reservation,
	violations map[hv1.ResourceName]resource.Quantity,
) *v1alpha1.Reservation {

	logger := LoggerFromContext(ctx).WithValues("component", "oversubscription-check")
	var selectedRes *v1alpha1.Reservation

	var unallocated, allocated []*v1alpha1.Reservation
	for i := range allReservations {
		res := &allReservations[i]
		if res.Spec.Type != v1alpha1.ReservationTypeCommittedResource {
			continue
		}
		if res.Spec.TargetHost == "" {
			continue // already evicted, pending status cleanup
		}
		if res.Spec.CommittedResourceReservation == nil ||
			len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
			unallocated = append(unallocated, res)
		} else {
			allocated = append(allocated, res)
		}
	}

	// Sort unallocated by memory asc, then CPU asc — smallest slots are easiest to re-place.
	sort.SliceStable(unallocated, func(i, j int) bool {
		ri, rj := unallocated[i].Spec.Resources, unallocated[j].Spec.Resources
		mi, mj := ri[hv1.ResourceMemory], rj[hv1.ResourceMemory]
		if c := mi.Cmp(mj); c != 0 {
			return c < 0
		}
		ci, cj := ri[hv1.ResourceCPU], rj[hv1.ResourceCPU]
		return ci.Cmp(cj) < 0
	})

	// First choice: smallest unallocated slot (easiest to re-place).
	if len(unallocated) > 0 {
		selectedRes = unallocated[0]
	}

	// Last resort: allocated slot with the largest fraction of unused capacity.
	if selectedRes == nil && len(allocated) > 0 {
		unusedCapRatioBuckets := make([]map[hv1.ResourceName]int64, len(allocated))
		for i, res := range allocated {
			unusedCap := reservations.UnusedReservationCapacity(res, false)
			buckets := make(map[hv1.ResourceName]int64)
			for rn, total := range res.Spec.Resources {
				buckets[rn] = unusedRatioBuckets(unusedCap[rn], total)
			}
			unusedCapRatioBuckets[i] = buckets
		}
		// Sort: 1st by unused mem ratio desc, 2nd by unused CPU ratio desc, 3rd by total mem asc.
		sort.SliceStable(allocated, func(i, j int) bool {
			if d := unusedCapRatioBuckets[i][hv1.ResourceMemory] - unusedCapRatioBuckets[j][hv1.ResourceMemory]; d != 0 {
				return d > 0
			}
			if d := unusedCapRatioBuckets[i][hv1.ResourceCPU] - unusedCapRatioBuckets[j][hv1.ResourceCPU]; d != 0 {
				return d > 0
			}
			mi, mj := allocated[i].Spec.Resources[hv1.ResourceMemory], allocated[j].Spec.Resources[hv1.ResourceMemory]
			return mi.Cmp(mj) < 0
		})
		selectedRes = allocated[0]
	}

	if selectedRes == nil {
		logger.Info("no eviction target found")
		return nil
	}
	logger.Info("eviction target selected",
		"reservation", selectedRes.Name,
		"total unallocated slots", len(unallocated),
		"total allocated slots", len(allocated),
		"slot resources", selectedRes.Spec.Resources,
		"violations", formatQuantityMap(violations),
	)
	return selectedRes
}

// checkHostOversubscription detects host over-subscription and evicts one slot if grace period elapsed.
// firstSeen is the time the violation was first detected (zero if not yet seen).
// Returns (evicted, resolved, err): evicted=slot was unplaced, resolved=no violation.
func (r *CommitmentReservationController) checkHostOversubscription(
	ctx context.Context,
	host string,
	allReservations []v1alpha1.Reservation,
	hv hv1.Hypervisor,
	monitor *ReservationControllerMonitor,
	gracePeriod time.Duration,
	firstSeen time.Time,
) (evicted, resolved bool, err error) {

	logger := LoggerFromContext(ctx).WithValues("component", "oversubscription-check", "host", host)
	az := hv.Labels["topology.kubernetes.io/zone"]

	violations := computeViolations(allReservations, hv)
	if violations == nil {
		monitor.ClearHost(host, az)
		return false, false, nil
	}
	monitor.ClearHost(host, az)

	if len(violations) == 0 {
		return false, true, nil
	}
	for rn, excess := range violations {
		monitor.SetOversubscribed(host, az, string(rn), float64(excess.Value()))
	}

	if firstSeen.IsZero() {
		logger.Info("host over-subscribed, starting grace period",
			"gracePeriod", gracePeriod,
			"violations", formatQuantityMap(violations))
		return false, false, nil
	}

	elapsed := time.Since(firstSeen)
	if elapsed < gracePeriod {
		logger.Info("host over-subscribed, grace period in progress",
			"elapsed", elapsed.Round(time.Second),
			"remaining", (gracePeriod - elapsed).Round(time.Second))
		return false, false, nil
	}

	logger.Info("host over-subscribed, evicting one slot", "violations", formatQuantityMap(violations))

	target := r.selectEvictionTarget(ctx, allReservations, violations)
	if target == nil {
		logger.Info("host over-subscribed but no evictable CR reservation slots found — manual intervention required",
			"violations", formatQuantityMap(violations))
		return false, false, nil
	}

	freed, err := r.unplaceReservation(ctx, target)
	if err != nil {
		logger.Error(err, "failed to unplace reservation for over-subscription remediation", "reservation", target.Name)
		return false, false, err
	}

	logger.Info("evicted slot for over-subscription remediation",
		"reservation", target.Name,
		"hasAllocations", target.Spec.CommittedResourceReservation != nil && len(target.Spec.CommittedResourceReservation.Allocations) > 0,
		"freed", formatQuantityMap(freed))
	return true, false, nil
}

// unplaceReservation clears Spec.TargetHost and Spec.Allocations, and returns the resources
// freed (full Spec.Resources — the slot is fully unplaced). Status cleanup is left to the
// reconcile loop, which will detect the Spec.TargetHost="" / IsReady() mismatch and converge.
func (r *CommitmentReservationController) unplaceReservation(
	ctx context.Context,
	res *v1alpha1.Reservation,
) (map[hv1.ResourceName]resource.Quantity, error) {

	logger := LoggerFromContext(ctx).WithValues("component", "oversubscription-check", "reservation", res.Name)

	freed := reservations.UnusedReservationCapacity(res, true)

	old := res.DeepCopy()
	res.Spec.TargetHost = ""
	if res.Spec.CommittedResourceReservation != nil {
		res.Spec.CommittedResourceReservation.Allocations = nil
	}
	logger.Info("unplacing reservation for over-subscription remediation",
		"freed", formatQuantityMap(freed),
		"previous host", old.Spec.TargetHost,
		"flavor", old.Spec.CommittedResourceReservation.ResourceName,
		"flavorGroup", old.Spec.CommittedResourceReservation.ResourceGroup,
		"hasAllocations", old.Spec.CommittedResourceReservation != nil && len(old.Spec.CommittedResourceReservation.Allocations) > 0)
	if err := r.Patch(ctx, res, client.MergeFrom(old)); err != nil {
		logger.Error(err, "failed to patch reservation", "reservation", res.Name)
		return nil, fmt.Errorf("failed to patch reservation %s: %w", res.Name, err)
	}
	return freed, nil
}

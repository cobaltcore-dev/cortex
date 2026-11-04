// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
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

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
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
	// Scheduler is the in-process scheduler used to place reservations.
	Scheduler *compute.Placement
	// DomainResolver resolves OpenStack domain IDs to domain names so the
	// domain_name scheduler hint can be populated for filter_external_customer.
	// Nil when KeystoneSecretRef is not configured; hint is omitted in that case.
	DomainResolver DomainResolver
	// VMSource is the source of truth for VM presence and actual VM resources.
	// Abstracts over the Nova DB today; will be swapped for a VM CRD when available.
	VMSource reservations.VMSource
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

	ctx = WithNewReconcileID(ctx)
	ctx = reservations.WithRequestID(ctx, req.Name)
	logger := LoggerFromContext(ctx)

	// filter for CR reservations
	resourceName := ""
	if res.Spec.CommittedResourceReservation != nil {
		resourceName = res.Spec.CommittedResourceReservation.ResourceName
	}
	if resourceName == "" {
		logger.Info("reservation has no resource name, skipping")
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "MissingResourceName",
			Message:            "reservation has no resource name",
			ObservedGeneration: res.Generation,
		})
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

	// Spec.TargetHost was cleared (e.g. oversubscription eviction) while the slot was confirmed —
	// revoke status so the slot re-enters the placement flow on the next reconcile.
	if res.Status.Host != "" && res.Spec.TargetHost == "" {
		old := res.DeepCopy()
		res.Status.Host = ""
		if res.Status.CommittedResourceReservation != nil {
			res.Status.CommittedResourceReservation.Allocation = nil
		}
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "PlacementRevoked",
			Message:            "target host was cleared; pending re-placement",
			ObservedGeneration: res.Generation,
		})
		if err := r.Status().Patch(ctx, &res, client.MergeFrom(old)); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		logger.Info("revoked ready status after placement eviction, slot re-enters placement flow",
			"component", "oversubscription-check")
		return ctrl.Result{}, nil
	}

	// Slot is placed and confirmed — verify allocations and keep ObservedGeneration current.
	if res.Spec.TargetHost != "" && res.Status.Host == res.Spec.TargetHost {
		// Deprecated: remove when Spec.CommittedResourceReservation.Allocations is removed from the schema.
		// One-time upgrade migration: blind-scheduler slots populated Spec.Allocations on the old schema.
		// Seed AnnotationCandidateAllocation from the lexically-first Spec entry so the existing
		// annotation recovery path re-confirms the VM on the next pass.
		if res.Spec.CommittedResourceReservation != nil &&
			res.Annotations[v1alpha1.AnnotationCandidateAllocation] == "" &&
			len(res.Spec.CommittedResourceReservation.Allocations) > 0 { //nolint:staticcheck
			keys := make([]string, 0, len(res.Spec.CommittedResourceReservation.Allocations)) //nolint:staticcheck
			for k := range res.Spec.CommittedResourceReservation.Allocations {                //nolint:staticcheck
				keys = append(keys, k)
			}
			sort.Strings(keys)
			vmUUID := keys[0]
			logger.Info("migrating first Spec allocation to annotation (one-time upgrade path)",
				"vm", vmUUID, "reservation", res.Name)
			old := res.DeepCopy()
			if res.Annotations == nil {
				res.Annotations = make(map[string]string)
			}
			res.Annotations[v1alpha1.AnnotationCandidateAllocation] = vmUUID
			if err := r.Patch(ctx, &res, client.MergeFrom(old)); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}

		logger.V(1).Info("reservation is active, verifying allocations")
		if err := r.reconcileAllocations(ctx, &res); err != nil {
			logger.Error(err, "failed to reconcile allocations")
			return ctrl.Result{}, err
		}
		// Refresh ObservedGeneration if the spec changed since the last reconcile
		// (e.g. endTime extension or metadata update via syncReservationMetadata).
		cond := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
		if cond == nil || cond.ObservedGeneration < res.Generation {
			old := res.DeepCopy()
			meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
				Type:               v1alpha1.ReservationConditionReady,
				Status:             metav1.ConditionTrue,
				Reason:             "ReservationActive",
				Message:            "reservation is successfully scheduled",
				ObservedGeneration: res.Generation,
			})
			if err := r.Status().Patch(ctx, &res, client.MergeFrom(old)); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalActive.Duration}, nil
	}

	// Sync Spec values to Status fields for non-pre-allocated reservations
	// This ensures the observed state reflects the desired state from Spec
	// When TargetHost is set in Spec but not synced to Status, this means
	// the scheduler found a host and we need to mark the reservation as ready.
	if res.Spec.TargetHost != "" && res.Status.Host != res.Spec.TargetHost {
		old := res.DeepCopy()
		res.Status.Host = res.Spec.TargetHost
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ReservationActive",
			Message:            "reservation is successfully scheduled",
			ObservedGeneration: res.Generation,
		})
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
		logger.V(1).Info("synced spec to status and marked ready", "host", res.Status.Host)
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

	// Resolve domain name so filter_external_customer can enforce host
	// restrictions for external customers. Resolution is best-effort: if the
	// DomainResolver is not configured (no KeystoneSecretRef) or the lookup
	// fails, we log and proceed without it. filter_external_customer handles a
	// missing domain name by skipping the filter, so omitting it degrades
	// gracefully rather than blocking scheduling.
	domainName := ""
	if r.DomainResolver != nil && res.Spec.CommittedResourceReservation != nil && res.Spec.CommittedResourceReservation.DomainID != "" {
		resolved, err := r.DomainResolver.ResolveDomainName(ctx, res.Spec.CommittedResourceReservation.DomainID)
		if err != nil {
			logger.Error(err, "failed to resolve domain name for scheduler hint, proceeding without it",
				"domainID", res.Spec.CommittedResourceReservation.DomainID)
		} else {
			domainName = resolved
		}
	}

	// Get flavor details from flavor group knowledge CRD
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.V(1).Info("flavor knowledge not ready, requeueing",
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
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "NoHostsAvailable",
			Message:            "no hypervisors available for scheduling",
			ObservedGeneration: res.Generation,
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry.Duration}, nil
	}

	// Select appropriate pipeline based on flavor group
	pipelineName := r.getPipelineForFlavorGroup(flavorGroupName, logger)
	logger.V(1).Info("selected pipeline for CR reservation",
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

	// Build the typed scheduler request directly (in-process; no HTTP). KVM
	// reservations gather all candidates, so the eligible-host list is a starting
	// set; GatherAllCandidates mirrors the previous ignore-preselection behavior.
	hosts := make([]compute.Host, 0, len(eligibleHosts))
	weights := make(map[string]float64, len(eligibleHosts))
	for _, h := range eligibleHosts {
		hosts = append(hosts, compute.Host{ComputeHost: h.ComputeHost, HypervisorHostname: h.HypervisorHostname})
		weights[h.ComputeHost] = 0.0
	}
	// Honor the flavor's trait and hypervisor-type constraints so a
	// committed-resource reservation lands on a compatible host (the typed
	// request is built directly, so filter_has_requested_traits/filter_capabilities
	// would otherwise see empty slices and become no-ops). No hypervisor-type
	// default here — only match when the flavor names one.
	requiredTraits, forbiddenTraits := compute.SplitTraits(flavorDetails.ExtraSpecs)
	schedReq := compute.PlacementRequest{
		Pipeline:              pipelineName,
		GatherAllCandidates:   true,
		HypervisorFamily:      compute.HypervisorFamilyKVM,
		Hosts:                 hosts,
		Weights:               weights,
		Intent:                schedulerdelegationapi.ReserveForCommittedResourceIntent,
		InstanceUUID:          res.Name,
		NumInstances:          1, // one reservation slot; NumInstances must be >= 1 or capacity/bin-pack math treats the host as free
		ProjectID:             projectID,
		AvailabilityZone:      availabilityZone,
		DomainName:            domainName,
		RequiredTraits:        requiredTraits,
		ForbiddenTraits:       forbiddenTraits,
		RequestedCapabilities: reservations.HypervisorTypeCapability(flavorDetails.ExtraSpecs, false),
		AcceleratorsRequested: compute.RequestsAccelerators(flavorDetails.ExtraSpecs),
		RequestedResources:    compute.NewResourceDemand(flavorDetails.VCPUs, flavorDetails.MemoryMB),
		Options: scheduling.Options{
			ReadOnly:                      false, // mutates state (reservation placement)
			LockReservations:              true,  // don't unlock CR reservations; finding a slot, not placing a VM
			MaxCandidates:                 1,
			SkipCommittedResourceTracking: true, // CR slot scheduling, not a VM placement
		},
	}

	out, err := r.Scheduler.Run(ctx, schedReq, nil, nil)
	if err != nil {
		logger.Error(err, "failed to schedule reservation")
		return ctrl.Result{}, err
	}

	if len(out.OrderedHosts) == 0 {
		logger.Info("no hosts found for reservation, will retry", "reservation", res.Name, "flavorName", resourceName)
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "NoHostsFound",
			Message:            "no hosts found for reservation",
			ObservedGeneration: res.Generation,
		})
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
	host := out.OrderedHosts[0]
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

// reconcileAllocations has two responsibilities:
//  1. Verify existing confirmed allocations via VMSource: VMs still on the expected
//     host have their partial allocation recomputed; VMs that have disappeared are removed.
//  2. Promote a PAYG candidate VM (AnnotationCandidateAllocation) into
//     status.allocation when it is confirmed active on the expected host.
func (r *CommitmentReservationController) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) error {
	logger := LoggerFromContext(ctx)

	if res.Spec.CommittedResourceReservation == nil {
		return nil
	}

	primaryResource := res.Spec.CommittedResourceReservation.PrimaryResource
	if primaryResource == "" {
		primaryResource = hv1.ResourceMemory
	}

	if res.Status.CommittedResourceReservation == nil {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
	}

	expectedHost := res.Status.Host

	newStatusAllocations := make(map[string]v1alpha1.CommittedResourceAllocation)

	// Pass 1: remove VMs no longer on the expected host.
	// Sort lexically for deterministic processing order for the greedy resource assignment logic.
	vmUUIDs := make([]string, 0, len(res.Status.CommittedResourceReservation.Allocation))
	for vmUUID := range res.Status.CommittedResourceReservation.Allocation {
		vmUUIDs = append(vmUUIDs, vmUUID)
	}
	// add VM id from annotation if present
	if candidateVMUUID := res.Annotations[v1alpha1.AnnotationCandidateAllocation]; candidateVMUUID != "" && !slices.Contains(vmUUIDs, candidateVMUUID) {
		logger.V(1).Info("adding candidate VM from annotation",
			"vm", candidateVMUUID,
			"reservation", res.Name)
		vmUUIDs = append(vmUUIDs, candidateVMUUID)
	}
	sort.Strings(vmUUIDs)

	survivingVMs := make(map[string]*reservations.VM, len(vmUUIDs))
	for _, vmUUID := range vmUUIDs {
		vm, err := r.VMSource.GetVM(ctx, vmUUID)
		if err != nil {
			// a failure of a VM lookup should not block the reconciliation of the reservation
			// instead, remove VM from allocations; if it was temporary failure, the VM will be consumed by some reservation eventually
			logger.V(1).Info("failed to get VM, removing from allocations",
				"vm", vmUUID,
				"reservation", res.Name,
				"err", err)
			continue
		}
		if vm == nil || vm.CurrentHypervisor != expectedHost {
			if _, ok := res.Status.CommittedResourceReservation.Allocation[vmUUID]; ok {
				logger.Info("removing confirmed allocation (VM not found on expected host)",
					"vm", vmUUID,
					"reservation", res.Name,
					"expectedHost", expectedHost)
			}
			if vmUUID == res.Annotations[v1alpha1.AnnotationCandidateAllocation] {
				logger.V(1).Info("candidate VM not found on expected host",
					"vm", vmUUID,
					"reservation", res.Name,
					"expectedHost", expectedHost)
			}
			continue
		}
		survivingVMs[vmUUID] = vm
	}

	// Pass 2: recompute allocation amounts for surviving VMs.
	// remainingSpare tracks slot capacity not yet assigned in this pass.
	remainingSpare := make(map[hv1.ResourceName]resource.Quantity, len(res.Spec.Resources))
	for rn, qty := range res.Spec.Resources {
		remainingSpare[rn] = qty.DeepCopy()
	}
	if len(remainingSpare) == 0 {
		logger.Info("Spec.Resources is empty — skipping allocation computation; no VMs will be confirmed", "reservation", res.Name)
	}
	zero := resource.MustParse("0")

	// Iterate in the same lexical order as pass 1.
	// assign resources greedily to lexically earlier VMs
	for _, vmUUID := range vmUUIDs {
		vm, ok := survivingVMs[vmUUID]
		if !ok {
			continue
		}
		vmResources := vmResourcesToHV(vm.Resources)
		alreadyElsewhere, err := r.computeAlreadyAssignedElsewhere(ctx, res, vmUUID)
		if err != nil {
			return fmt.Errorf("failed to compute already-assigned for vm %s: %w", vmUUID, err)
		}
		vmRemaining := subtractResourceQuantitiesClamped(vmResources, alreadyElsewhere)
		newAlloc := minResourceQuantities(remainingSpare, vmRemaining)
		newAllocPrimary := newAlloc[primaryResource]
		if len(newAlloc) == 0 || newAllocPrimary.IsZero() {
			logger.V(1).Info("removing VM allocation: fully covered by other slots",
				"vm", vmUUID, "reservation", res.Name)
			continue
		}
		newStatusAllocations[vmUUID] = v1alpha1.CommittedResourceAllocation{
			// TODO replace with VM CRD ref once the VM CRD is strictly considered as being the mandatory gate
			ObjectRef: corev1.ObjectReference{Name: vmUUID},
			Resources: newAlloc,
		}
		for rn, qty := range newAlloc {
			if spare, ok := remainingSpare[rn]; ok {
				spare.Sub(qty)
				if spare.Cmp(zero) < 0 {
					spare = zero.DeepCopy()
				}
				remainingSpare[rn] = spare
			}
		}
		// is the VM fully covered by slots, or do we need more reservations?
		if vmRemainingPrimary := vmRemaining[primaryResource]; len(vmRemaining) == 0 || vmRemainingPrimary.Cmp(newAllocPrimary) > 0 {
			logger.V(1).Info("VM not fully covered by slots, may need more reservations",
				"vm", vmUUID,
				"reservation", res.Name,
				"vmRemaining", vmRemaining,
				"newAlloc", newAlloc,
			)
		}
	}

	// Single status patch.
	old := res.DeepCopy()
	// Skip the status patch when allocations have not changed.
	if apiequality.Semantic.DeepEqual(res.Status.CommittedResourceReservation.Allocation, newStatusAllocations) {
		logger.V(1).Info("reconciled allocations, nothing has changed")
		return nil
	}
	res.Status.CommittedResourceReservation.Allocation = newStatusAllocations
	if err := r.Status().Patch(ctx, res, client.MergeFrom(old)); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("reservation not found, nothing to patch, probably deleted in the meantime", "reservation", res.Name)
			return nil
		}
		return fmt.Errorf("failed to patch reservation status: %w", err)
	}
	logger.V(1).Info("reconciled allocations", "remainingSpare", remainingSpare, "statusAllocations", len(newStatusAllocations))
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

	logger.V(1).Info("no pipeline configured for flavor group, using default", "flavorGroup", flavorGroupName, "defaultPipeline", r.Conf.PipelineDefault)
	return r.Conf.PipelineDefault
}

// computeAlreadyAssignedElsewhere returns the total resources already assigned to vmUUID
// by OTHER confirmed slots on the same host.
// Used to compute the correct partial amount when a VM may span multiple reservation slots.
func (r *CommitmentReservationController) computeAlreadyAssignedElsewhere(
	ctx context.Context,
	res *v1alpha1.Reservation,
	vmUUID string,
) (map[hv1.ResourceName]resource.Quantity, error) {

	var conflicts v1alpha1.ReservationList
	if err := r.List(ctx, &conflicts, client.MatchingFields{idxReservationByAllocationVMUUID: vmUUID}); err != nil {
		return nil, fmt.Errorf("failed to query allocation index for vm %s: %w", vmUUID, err)
	}
	result := make(map[hv1.ResourceName]resource.Quantity)
	for i := range conflicts.Items {
		other := &conflicts.Items[i]
		if other.Name == res.Name && other.Namespace == res.Namespace {
			continue
		}
		if other.Status.CommittedResourceReservation == nil {
			// unreachable path
			continue
		}
		if other.Status.Host != res.Status.Host {
			// stale allocation on a different-host slot — skip; it will self-correct when that slot next reconciles.
			continue // only same-host slots contribute
		}
		if alloc, ok := other.Status.CommittedResourceReservation.Allocation[vmUUID]; ok {
			for rn, qty := range alloc.Resources {
				if cur, exists := result[rn]; exists {
					cur.Add(qty)
					result[rn] = cur
				} else {
					result[rn] = qty.DeepCopy()
				}
			}
		}
	}
	return result, nil
}

// vmResourcesToHV converts VM.Resources (plain string keys from Nova DB)
// to the hv1.ResourceName-keyed format used by reservation slots.
func vmResourcesToHV(r map[string]resource.Quantity) map[hv1.ResourceName]resource.Quantity {
	out := make(map[hv1.ResourceName]resource.Quantity, len(r))
	for k, v := range r {
		out[hv1.ResourceName(k)] = v
	}
	return out
}

// minResourceQuantities returns the component-wise minimum of two resource maps.
// For resources present in one map but not the other, the missing side defaults to zero.
func minResourceQuantities(a, b map[hv1.ResourceName]resource.Quantity) map[hv1.ResourceName]resource.Quantity {
	zero := resource.MustParse("0")
	all := make(map[hv1.ResourceName]struct{})
	for rn := range a {
		all[rn] = struct{}{}
	}
	for rn := range b {
		all[rn] = struct{}{}
	}
	result := make(map[hv1.ResourceName]resource.Quantity, len(all))
	for rn := range all {
		av, aok := a[rn]
		if !aok {
			av = zero
		}
		bv, bok := b[rn]
		if !bok {
			bv = zero
		}
		if av.Cmp(bv) <= 0 {
			result[rn] = av.DeepCopy()
		} else {
			result[rn] = bv.DeepCopy()
		}
	}
	return result
}

// subtractResourceQuantitiesClamped returns max(a[rn] - b[rn], 0) for each resource in a.
func subtractResourceQuantitiesClamped(a, b map[hv1.ResourceName]resource.Quantity) map[hv1.ResourceName]resource.Quantity {
	zero := resource.MustParse("0")
	result := make(map[hv1.ResourceName]resource.Quantity, len(a))
	for rn, av := range a {
		diff := av.DeepCopy()
		if bv, ok := b[rn]; ok {
			diff.Sub(bv)
		}
		if diff.Cmp(zero) < 0 {
			diff = zero.DeepCopy()
		}
		result[rn] = diff
	}
	return result
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

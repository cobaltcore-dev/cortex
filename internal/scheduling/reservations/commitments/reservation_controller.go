// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
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

	ctx = WithNewGlobalRequestID(ctx)
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
			res.Status.CommittedResourceReservation.Allocations = nil
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

// reconcileAllocations verifies all confirmed allocations in Status against actual VM state
// using the Hypervisor CRD as the sole source of truth.
//
// Confirmed VMs that disappear from their expected host are handled as follows:
//
// Live migration: when a confirmed VM is found on a different host, the reservation follows
// it only when the reservation has exactly one allocated VM and the new host has capacity.
// In all other cases (multiple VMs, or new host at capacity), the migrated VM is removed
// from the reservation so the slot remains available for re-use on the original host.
// Moving TargetHost when other VMs are present would cause those remaining VMs to appear
// misplaced on the next reconcile cycle.
func (r *CommitmentReservationController) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) error {
	logger := LoggerFromContext(ctx)

	// Skip if no CommittedResourceReservation
	if res.Spec.CommittedResourceReservation == nil {
		return nil
	}

	// Initialize status if needed
	if res.Status.CommittedResourceReservation == nil {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
	}

	// Skip if no confirmed allocations to verify.
	// Exception: if the AnnotationCandidateAllocation annotation is set, this is a PAYG pre-allocated slot;
	// verify the candidate VM on the HV CRD and initialize status.allocations if confirmed.
	if len(res.Status.CommittedResourceReservation.Allocations) == 0 {
		candidateVMUUID := res.Annotations[v1alpha1.AnnotationCandidateAllocation]
		if candidateVMUUID == "" || res.Status.Host == "" {
			logger.V(1).Info("no allocations to verify", "reservation", res.Name)
			return nil
		}
		// Check if candidate VM is active on the expected host.
		var hypervisor hv1.Hypervisor
		if err := r.Get(ctx, client.ObjectKey{Name: res.Status.Host}, &hypervisor); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to get hypervisor %s: %w", res.Status.Host, err)
			}
			logger.V(1).Info("candidate VM host not found", "host", res.Status.Host, "vm", candidateVMUUID)
			return nil
		}
		for _, inst := range hypervisor.Status.Instances {
			if inst.ID == candidateVMUUID && inst.Active {
				old := res.DeepCopy()
				res.Status.CommittedResourceReservation.Allocations = map[string]v1alpha1.CommittedResourceAllocation{
					candidateVMUUID: {
						ObjectRef: corev1.ObjectReference{Name: candidateVMUUID},
						Resources: res.Spec.Resources,
					},
				}
				if err := r.Status().Patch(ctx, res, client.MergeFrom(old)); err != nil {
					if client.IgnoreNotFound(err) == nil {
						return nil
					}
					return fmt.Errorf("failed to patch status allocations for candidate VM: %w", err)
				}
				logger.Info("confirmed PAYG candidate VM via Hypervisor CRD",
					"vm", candidateVMUUID,
					"host", res.Status.Host,
					"reservation", res.Name,
				)
				break
			}
		}
		return nil
	}

	expectedHost := res.Status.Host

	// Fetch the Hypervisor CRD for the expected host.
	var hypervisor hv1.Hypervisor
	hvInstanceSet := make(map[string]bool)
	if expectedHost != "" {
		if err := r.Get(ctx, client.ObjectKey{Name: expectedHost}, &hypervisor); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to get hypervisor %s: %w", expectedHost, err)
			}
			// Hypervisor not found — treat all VMs as stale.
			logger.V(1).Info("hypervisor CRD not found", "host", expectedHost)
		} else {
			for _, inst := range hypervisor.Status.Instances {
				hvInstanceSet[inst.ID] = true
			}
			logger.V(1).Info("fetched hypervisor instances", "host", expectedHost, "instanceCount", len(hvInstanceSet))
		}
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
		resList := &v1alpha1.ReservationList{}
		if err := r.List(ctx, resList); err != nil {
			return fmt.Errorf("failed to list reservations: %w", err)
		}
		allHVs = hvs
		allReservations = resList
		return nil
	}

	// Build new Status.Allocations map based on HV CRD state.
	newStatusAllocations := make(map[string]v1alpha1.CommittedResourceAllocation)

	// migrationTargetHost is set when the reservation has exactly one VM, that VM
	// live-migrated to a new host, and the new host has capacity.
	migrationTargetHost := ""

	isSingleVM := len(res.Status.CommittedResourceReservation.Allocations) == 1

	for vmUUID := range res.Status.CommittedResourceReservation.Allocations {
		// Double-allocation check: if another reservation already has this VM confirmed
		// in its status.allocations, resolve by lexical name order — the reservation
		// with the lexically-earlier name keeps the VM; the other removes it.
		var conflicts v1alpha1.ReservationList
		if err := r.List(ctx, &conflicts, client.MatchingFields{idxReservationByAllocationVMUUID: vmUUID}); err != nil {
			logger.Error(err, "failed to query reservation index for double-allocation check", "vmUUID", vmUUID)
		} else {
			evicted := false
			for _, other := range conflicts.Items {
				if other.Name == res.Name && other.Namespace == res.Namespace {
					continue
				}
				if other.Name < res.Name {
					logger.Info("double allocation detected: evicting VM (other reservation wins by lexical order)",
						"vmUUID", vmUUID, "winnerReservation", other.Name, "loserReservation", res.Name)
					evicted = true
					break
				}
			}
			if evicted {
				continue // don't add to newStatusAllocations; evicted VM falls out on next status patch
			}
		}

		// VM still on expected host — confirmed.
		if hvInstanceSet[vmUUID] {
			newStatusAllocations[vmUUID] = v1alpha1.CommittedResourceAllocation{
				ObjectRef: corev1.ObjectReference{Name: vmUUID},
				Resources: res.Spec.Resources,
			}
			logger.V(1).Info("verified VM allocation via Hypervisor CRD",
				"vm", vmUUID,
				"host", expectedHost)
			continue
		}

		// Confirmed VM missing from expected host — could be a live migration.
		if err := ensureHVsAndReservations(); err != nil {
			return err
		}

		var foundHost string
		var foundHV hv1.Hypervisor
		for _, hv := range allHVs.Items {
			if hv.Name == expectedHost {
				continue
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
			// VM is not on any known hypervisor — terminated or evacuated.
			// A VM CRD with lifecycle state (migrating/active) would close the
			// narrow race window where a VM is mid-live-migration; without one
			// we accept this as a known limitation.
			logger.Info("removing confirmed allocation (VM not found on any hypervisor)",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost)
			continue
		}

		// VM found on a different host — live migration detected.
		if isSingleVM && reservations.HostHasCapacityForReservation(allReservations.Items, foundHV, res) {
			logger.Info("VM live-migrated to host with capacity, updating TargetHost",
				"vm", vmUUID,
				"reservation", res.Name,
				"oldHost", expectedHost,
				"newHost", foundHost)
			migrationTargetHost = foundHost
			newStatusAllocations[vmUUID] = v1alpha1.CommittedResourceAllocation{
				ObjectRef: corev1.ObjectReference{Name: vmUUID},
				Resources: res.Spec.Resources,
			}
		} else {
			logger.Info("removing VM from reservation after live migration: either multiple VMs present or new host lacks capacity",
				"vm", vmUUID,
				"reservation", res.Name,
				"expectedHost", expectedHost,
				"actualHost", foundHost,
				"singleVM", isSingleVM)
		}
	}

	// Patch Spec if TargetHost needs updating (live migration follow)
	old := res.DeepCopy()
	specChanged := migrationTargetHost != ""
	if specChanged {
		res.Spec.TargetHost = migrationTargetHost
		res.Status.Host = migrationTargetHost
		if err := r.Patch(ctx, res, client.MergeFrom(old)); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
			return fmt.Errorf("failed to patch reservation spec: %w", err)
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(res), res); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
			return fmt.Errorf("failed to re-fetch reservation: %w", err)
		}
		old = res.DeepCopy()
		res.Status.CommittedResourceReservation.Allocations = newStatusAllocations
		res.Status.Host = migrationTargetHost
	}

	// Update Status.Allocations
	res.Status.CommittedResourceReservation.Allocations = newStatusAllocations

	// Patch Status
	if err := r.Status().Patch(ctx, res, client.MergeFrom(old)); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return fmt.Errorf("failed to patch reservation status: %w", err)
	}

	logger.V(1).Info("reconciled allocations",
		"statusAllocations", len(newStatusAllocations))

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

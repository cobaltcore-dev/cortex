// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var log = ctrl.Log.WithName("failover-reservation-controller").WithValues("module", "reservations/failover")

// FailoverReservationController manages failover reservations for VMs.
// It provides two reconciliation modes:
// 1. Periodic bulk reconciliation (ReconcilePeriodic) - processes all VMs to ensure proper failover coverage
// 2. Watch-based per-reservation reconciliation (Reconcile) - handles acknowledgment and validation of individual reservations
type FailoverReservationController struct {
	client.Client
	VMSource        VMSource
	Config          FailoverConfig
	SchedulerClient *reservations.SchedulerClient
	Recorder        record.EventRecorder // Event recorder for emitting Kubernetes events
	reconcileCount  int64                // Track reconciliation count for rotating VM selection
}

func NewFailoverReservationController(c client.Client, vmSource VMSource, config FailoverConfig, schedulerClient *reservations.SchedulerClient) *FailoverReservationController {
	return &FailoverReservationController{
		Client:          c,
		VMSource:        vmSource,
		Config:          config,
		SchedulerClient: schedulerClient,
	}
}

type vmFailoverNeed struct {
	VM    VM
	Count int // Number of failover reservations needed
}

// ============================================================================
// Watch-based Reconciliation (per-reservation)
// ============================================================================

// Reconcile handles watch-based reconciliation for a single failover reservation.
// It validates the reservation and acknowledges it if valid, or deletes it if invalid.
// After processing, it requeues for periodic re-validation.
func (c *FailoverReservationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Generate a random UUID for request tracking
	globalReqID := uuid.New().String()
	ctx = reservations.WithGlobalRequestID(ctx, globalReqID)
	logger := LoggerFromContext(ctx).WithValues("reservation", req.Name, "namespace", req.Namespace)
	logger.Info("reconciling failover reservation", "reservation", req.Name)

	// Fetch the reservation
	var res v1alpha1.Reservation
	if err := c.Get(ctx, req.NamespacedName, &res); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("reservation not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get reservation")
		return ctrl.Result{}, err
	}

	// Skip non-failover reservations (should be filtered by predicate, but double-check)
	if res.Spec.Type != v1alpha1.ReservationTypeFailover {
		logger.V(1).Info("skipping non-failover reservation")
		return ctrl.Result{}, nil
	}

	// Skip if no failover status (reservation not yet initialized by periodic controller)
	if res.Status.FailoverReservation == nil {
		logger.V(1).Info("skipping reservation without failover status")
		return ctrl.Result{RequeueAfter: c.Config.RevalidationInterval}, nil
	}

	// Validate and acknowledge the reservation
	return c.reconcileValidateAndAcknowledge(ctx, &res)
}

// reconcileValidateAndAcknowledge validates a reservation and acknowledges it if valid.
// If invalid, the reservation is marked as not ready. On transient errors, the reservation is requeued.
// On success, AcknowledgedAt is always updated.
func (c *FailoverReservationController) reconcileValidateAndAcknowledge(ctx context.Context, res *v1alpha1.Reservation) (ctrl.Result, error) {
	logger := LoggerFromContext(ctx).WithValues("reservation", res.Name)
	logger.V(1).Info("validating failover reservation")

	// Validate resource keys first (must be "cpu" and "memory" only)
	if err := ValidateFailoverReservationResources(res); err != nil {
		logger.Info("reservation has invalid resources, marking as not ready", "error", err)

		updatedRes := res.DeepCopy()
		meta.SetStatusCondition(&updatedRes.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidResources",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})

		if patchErr := c.patchReservationStatus(ctx, updatedRes); patchErr != nil {
			logger.Error(patchErr, "failed to update reservation status for invalid resources")
			return ctrl.Result{}, patchErr
		}

		return ctrl.Result{RequeueAfter: c.Config.RevalidationInterval}, nil
	}

	// Validate the reservation
	valid, validationErr := c.validateReservation(ctx, res)

	if validationErr != nil {
		logger.Error(validationErr, "transient error during reservation validation, will retry", "host", res.Status.Host)
		return ctrl.Result{RequeueAfter: c.Config.RevalidationInterval}, nil
	}

	if !valid {
		logger.Info("reservation validation failed, deleting", "host", res.Status.Host)
		if err := c.Delete(ctx, res); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			logger.Error(err, "failed to delete invalid reservation")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Emit event for successful validation (doesn't trigger watch reconciliation)
	c.Recorder.Event(res, corev1.EventTypeNormal, "ValidationPassed",
		fmt.Sprintf("Reservation validated successfully for host %s with %d VMs",
			res.Status.Host, len(getFailoverAllocations(res))))

	// Only update AcknowledgedAt if there are unacknowledged changes
	lastChanged := res.Status.FailoverReservation.LastChanged
	acknowledgedAt := res.Status.FailoverReservation.AcknowledgedAt
	if lastChanged != nil && (acknowledgedAt == nil || acknowledgedAt.Before(lastChanged)) {
		updatedRes := res.DeepCopy()
		now := metav1.Now()
		updatedRes.Status.FailoverReservation.AcknowledgedAt = &now

		if err := c.patchReservationStatus(ctx, updatedRes); err != nil {
			logger.Error(err, "failed to update reservation acknowledgment")
			return ctrl.Result{}, err
		}

		logger.V(1).Info("reservation changes acknowledged", "host", res.Status.Host, "lastChanged", lastChanged, "acknowledgedAt", now)
	} else {
		logger.V(1).Info("reservation validation passed (no new changes to acknowledge)", "host", res.Status.Host)
	}

	return ctrl.Result{RequeueAfter: c.Config.RevalidationInterval}, nil
}

// validateReservation validates that a reservation is still valid for all its allocated VMs.
// Returns:
//   - (true, nil) if all VMs pass validation
//   - (false, nil) if any VM definitively fails validation (reservation should be deleted)
//   - (false, error) if a transient error occurred (reservation should be requeued, not deleted)
func (c *FailoverReservationController) validateReservation(ctx context.Context, res *v1alpha1.Reservation) (bool, error) {
	logger := LoggerFromContext(ctx).WithValues("reservationName", res.Name)
	allocations := getFailoverAllocations(res)
	if len(allocations) == 0 {
		return true, nil
	}

	reservationHost := res.Status.Host
	if reservationHost == "" {
		logger.Info("reservation has no host, marking as invalid")
		return false, nil
	}

	logger.V(1).Info("validating reservation", "host", reservationHost, "vmCount", len(allocations))

	for vmUUID, vmCurrentHost := range allocations {
		vmCtx := reservations.WithRequestID(ctx, vmUUID)
		vmLogger := LoggerFromContext(vmCtx).WithValues("vmUUID", vmUUID, "reservationName", res.Name)

		vm, err := c.VMSource.GetVM(vmCtx, vmUUID)
		if err != nil {
			vmLogger.Error(err, "transient error getting VM for validation")
			return false, fmt.Errorf("failed to get VM %s: %w", vmUUID, err)
		}
		if vm == nil {
			vmLogger.V(1).Info("VM not found during validation, skipping")
			continue
		}

		valid, err := c.validateVMViaSchedulerEvacuation(vmCtx, *vm, reservationHost)
		if err != nil {
			vmLogger.Error(err, "transient error validating VM for reservation host", "reservationHost", reservationHost)
			return false, fmt.Errorf("failed to validate VM %s: %w", vmUUID, err)
		}

		if !valid {
			vmLogger.Info("VM failed validation for reservation host", "vmCurrentHost", vmCurrentHost, "reservationHost", reservationHost)
			return false, nil
		}

		vmLogger.V(1).Info("VM passed validation for reservation host", "reservationHost", reservationHost)
	}

	return true, nil
}

// ============================================================================
// Periodic Bulk Reconciliation
// ============================================================================

// ReconcilePeriodic handles the periodic bulk reconciliation of all VMs and reservations.
// This ensures VMs have proper failover coverage by creating, reusing, and cleaning up reservations.
// TODO consider moving Step 3-5 (particularly) to the watch-based reconciliation
func (c *FailoverReservationController) ReconcilePeriodic(ctx context.Context) (ctrl.Result, error) {
	c.reconcileCount++
	globalReqID := uuid.New().String()
	ctx = reservations.WithGlobalRequestID(ctx, globalReqID)
	logger := LoggerFromContext(ctx)

	logger.Info("running periodic reconciliation", "reconcileCount", c.reconcileCount)

	// 1. Get hypervisors from the cluster
	var hypervisorList hv1.HypervisorList
	if err := c.List(ctx, &hypervisorList); err != nil {
		logger.Error(err, "failed to list hypervisors")
		return ctrl.Result{}, err
	}

	allHypervisors := make([]string, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		allHypervisors = append(allHypervisors, hv.Name)
	}

	// 2. Get all VMs that might need failover reservations
	vms, err := c.VMSource.ListVMsOnHypervisors(ctx, &hypervisorList, c.Config.TrustHypervisorLocation)
	if err != nil {
		logger.Error(err, "failed to list VMs")
		return ctrl.Result{}, err
	}
	logger.Info("found VMs from source", "count", len(vms))

	// List only failover reservations using label selector
	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailover,
	}); err != nil {
		logger.Error(err, "failed to list failover reservations")
		return ctrl.Result{}, err
	}
	failoverReservations := reservationList.Items
	logger.Info("found failover reservations", "count", len(failoverReservations))

	// 3. Remove VMs from reservations if they are no longer valid
	failoverReservations, reservationsToUpdate := reconcileRemoveInvalidVMFromReservations(ctx, vms, failoverReservations)

	for _, res := range reservationsToUpdate {
		if err := c.patchReservationStatus(ctx, res); err != nil {
			logger.Error(err, "failed to update reservation after removing invalid VMs", "reservationName", res.Name)
		}
	}
	if len(reservationsToUpdate) > 0 {
		logger.Info("updated reservations after removing invalid VMs", "count", len(reservationsToUpdate))
	}

	// 4. Remove VMs from reservations if they no longer meet eligibility criteria
	failoverReservations, nonEligibleReservationsToUpdate := reconcileRemoveNoneligibleVMFromReservations(ctx, vms, failoverReservations)

	for _, res := range nonEligibleReservationsToUpdate {
		if err := c.patchReservationStatus(ctx, res); err != nil {
			logger.Error(err, "failed to update reservation after removing non-eligible VMs", "reservationName", res.Name)
		}
	}
	if len(nonEligibleReservationsToUpdate) > 0 {
		logger.Info("updated reservations after removing non-eligible VMs", "count", len(nonEligibleReservationsToUpdate))
	}

	// 5. Remove empty failover reservations
	failoverReservations, emptyReservationsToDelete := reconcileRemoveEmptyReservations(ctx, failoverReservations)

	for _, res := range emptyReservationsToDelete {
		if err := c.Delete(ctx, res); err != nil {
			logger.Error(err, "failed to delete empty failover reservation", "reservationName", res.Name)
		} else {
			logger.Info("deleted empty failover reservation", "reservationName", res.Name, "hypervisor", res.Status.Host)
		}
	}
	if len(emptyReservationsToDelete) > 0 {
		logger.Info("deleted empty failover reservations", "count", len(emptyReservationsToDelete))
	}

	// 6. Create and assign reservations for VMs that need them
	err, hitMaxVMsLimit := c.reconcileCreateAndAssignReservations(ctx, vms, failoverReservations, allHypervisors)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("completed periodic reconciliation")

	if hitMaxVMsLimit && c.Config.ShortReconcileInterval > 0 {
		logger.Info("requeuing with short interval due to MaxVMsToProcess limit", "shortReconcileInterval", c.Config.ShortReconcileInterval)
		return ctrl.Result{RequeueAfter: c.Config.ShortReconcileInterval}, nil
	}

	return ctrl.Result{RequeueAfter: c.Config.ReconcileInterval}, nil
}

// reconcileRemoveInvalidVMFromReservations removes VMs from reservation allocations if:
// - The VM no longer exists
// - The VM has moved to a different host
// Returns the updated list of reservations (with modifications applied in-memory).
// The caller is responsible for persisting any changes to the cluster.
func reconcileRemoveInvalidVMFromReservations(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToUpdate []*v1alpha1.Reservation) {

	logger := LoggerFromContext(ctx)

	vmToHypervisor := make(map[string]string)
	for _, vm := range vms {
		vmToHypervisor[vm.UUID] = vm.CurrentHypervisor
	}

	updatedReservations = make([]v1alpha1.Reservation, 0, len(failoverReservations))

	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		updatedAllocations := make(map[string]string)
		needsUpdate := false

		for vmUUID, allocatedHypervisor := range allocations {
			vmCurrentHypervisor, vmExists := vmToHypervisor[vmUUID]
			if !vmExists {
				logger.Info("removing VM from reservation allocations because VM no longer exists",
					"vmUUID", vmUUID, "reservation", res.Name)
				needsUpdate = true
				continue
			}
			if vmCurrentHypervisor != allocatedHypervisor {
				logger.Info("removing VM from reservation allocations because hypervisor has changed",
					"vmUUID", vmUUID, "reservation", res.Name,
					"allocatedHypervisor", allocatedHypervisor, "currentHypervisor", vmCurrentHypervisor)
				needsUpdate = true
				continue
			}
			updatedAllocations[vmUUID] = allocatedHypervisor
		}

		if needsUpdate {
			updatedRes := res.DeepCopy()
			if updatedRes.Status.FailoverReservation == nil {
				updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
			}
			updatedRes.Status.FailoverReservation.Allocations = updatedAllocations
			now := metav1.Now()
			updatedRes.Status.FailoverReservation.LastChanged = &now
			updatedRes.Status.FailoverReservation.AcknowledgedAt = nil
			updatedReservations = append(updatedReservations, *updatedRes)
			reservationsToUpdate = append(reservationsToUpdate, updatedRes)
		} else {
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToUpdate
}

// reconcileRemoveNoneligibleVMFromReservations removes VMs from reservation allocations if
// they no longer meet eligibility criteria.
func reconcileRemoveNoneligibleVMFromReservations(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToUpdate []*v1alpha1.Reservation) {

	logger := LoggerFromContext(ctx)

	vmByUUID := make(map[string]VM)
	for _, vm := range vms {
		vmByUUID[vm.UUID] = vm
	}

	updatedReservations = make([]v1alpha1.Reservation, 0, len(failoverReservations))

	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		updatedAllocations := make(map[string]string)
		needsUpdate := false

		for vmUUID, allocatedHypervisor := range allocations {
			vm, vmExists := vmByUUID[vmUUID]
			if !vmExists {
				updatedAllocations[vmUUID] = allocatedHypervisor
				continue
			}

			tempRes := res.DeepCopy()
			delete(tempRes.Status.FailoverReservation.Allocations, vmUUID)

			tempReservations := make([]v1alpha1.Reservation, 0, len(failoverReservations))
			for _, r := range failoverReservations {
				if r.Name == res.Name {
					tempReservations = append(tempReservations, *tempRes)
				} else {
					tempReservations = append(tempReservations, r)
				}
			}

			if !IsVMEligibleForReservation(vm, *tempRes, tempReservations) {
				logger.Info("removing VM from reservation allocations because it no longer meets eligibility criteria",
					"vmUUID", vmUUID, "reservation", res.Name,
					"vmHypervisor", vm.CurrentHypervisor, "reservationHypervisor", res.Status.Host)
				needsUpdate = true
				continue
			}
			updatedAllocations[vmUUID] = allocatedHypervisor
		}

		if needsUpdate {
			updatedRes := res.DeepCopy()
			if updatedRes.Status.FailoverReservation == nil {
				updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
			}
			updatedRes.Status.FailoverReservation.Allocations = updatedAllocations
			now := metav1.Now()
			updatedRes.Status.FailoverReservation.LastChanged = &now
			updatedRes.Status.FailoverReservation.AcknowledgedAt = nil
			updatedReservations = append(updatedReservations, *updatedRes)
			reservationsToUpdate = append(reservationsToUpdate, updatedRes)
		} else {
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToUpdate
}

// reconcileRemoveEmptyReservations removes failover reservations that have no allocated VMs.
func reconcileRemoveEmptyReservations(
	ctx context.Context,
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToDelete []*v1alpha1.Reservation) {

	logger := LoggerFromContext(ctx)

	updatedReservations = make([]v1alpha1.Reservation, 0, len(failoverReservations))

	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		if len(allocations) == 0 {
			resCopy := res.DeepCopy()
			reservationsToDelete = append(reservationsToDelete, resCopy)
			logger.Info("marking empty failover reservation for deletion", "reservationName", res.Name, "hypervisor", res.Status.Host)
		} else {
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToDelete
}

// selectVMsToProcess selects a subset of VMs to process based on MaxVMsToProcess limit.
func (c *FailoverReservationController) selectVMsToProcess(
	ctx context.Context,
	vmsMissingFailover []vmFailoverNeed,
	maxToProcess int,
) (selected []vmFailoverNeed, hitLimit bool) {

	logger := LoggerFromContext(ctx)

	if len(vmsMissingFailover) == 0 {
		return vmsMissingFailover, false
	}

	sortVMsByMemory(vmsMissingFailover)

	if maxToProcess <= 0 || len(vmsMissingFailover) <= maxToProcess {
		return vmsMissingFailover, false
	}

	offset := 0
	if c.reconcileCount%4 == 0 {
		offset = int(c.reconcileCount) % len(vmsMissingFailover)
	}

	selected = make([]vmFailoverNeed, 0, maxToProcess)
	for i := range maxToProcess {
		idx := (offset + i) % len(vmsMissingFailover)
		selected = append(selected, vmsMissingFailover[idx])
	}

	logger.Info("selected VMs to process (sorted by memory, with rotation)",
		"totalVMsMissingFailover", len(vmsMissingFailover),
		"maxToProcess", maxToProcess,
		"offset", offset,
		"reconcileCount", c.reconcileCount)

	return selected, true
}

// sortVMsByMemory sorts VMs by memory in descending order (largest first).
func sortVMsByMemory(vms []vmFailoverNeed) {
	sort.Slice(vms, func(i, j int) bool {
		var memI, memJ int64
		if mem, ok := vms[i].VM.Resources["memory"]; ok {
			memI = mem.Value()
		}
		if mem, ok := vms[j].VM.Resources["memory"]; ok {
			memJ = mem.Value()
		}
		return memI > memJ
	})
}

// reconcileCreateAndAssignReservations creates and assigns failover reservations for VMs that need them.
//
//nolint:unparam // error return is intentionally always nil - we log errors but continue processing
func (c *FailoverReservationController) reconcileCreateAndAssignReservations(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
	allHypervisors []string,
) (error, bool) {

	logger := LoggerFromContext(ctx)

	vmsMissingFailover := c.calculateVMsMissingFailover(ctx, vms, failoverReservations)
	logger.Info("VMs missing failover reservations", "count", len(vmsMissingFailover))

	vmsMissingFailover, hitMaxVMsLimit := c.selectVMsToProcess(ctx, vmsMissingFailover, c.Config.MaxVMsToProcess)

	logger.Info("found hypervisors and vm missing failover reservation",
		"countHypervisors", len(allHypervisors),
		"countVMsMissingFailover", len(vmsMissingFailover))

	totalReservationsNeeded := 0
	for _, need := range vmsMissingFailover {
		totalReservationsNeeded += need.Count
	}

	var totalReused, totalCreated, totalFailed int

	for _, need := range vmsMissingFailover {
		vmReused := 0
		vmCreated := 0
		vmFailed := 0

		reqID := uuid.New().String()
		vmCtx := reservations.WithRequestID(ctx, reqID)
		vmLogger := LoggerFromContext(vmCtx).WithValues("vmUUID", need.VM.UUID)
		vmLogger.Info("processing VM for failover reservation")

		for i := range need.Count {
			reusedRes := c.tryReuseExistingReservation(vmCtx, need.VM, failoverReservations, allHypervisors)

			if reusedRes != nil {
				if err := c.patchReservationStatus(vmCtx, reusedRes); err != nil {
					vmLogger.Error(err, "failed to persist reused reservation", "reservationName", reusedRes.Name)
					vmFailed++
					continue
				}
				vmReused++
				for j := range failoverReservations {
					if failoverReservations[j].Name == reusedRes.Name {
						failoverReservations[j] = *reusedRes
						break
					}
				}
				continue
			}

			newRes, err := c.scheduleAndBuildNewFailoverReservation(vmCtx, need.VM, allHypervisors, failoverReservations)
			if err != nil {
				vmLogger.V(1).Info("failed to schedule failover reservation", "error", err, "iteration", i+1, "needed", need.Count)
				vmFailed++
				break
			}

			savedStatus := newRes.Status.DeepCopy()

			if err := c.Create(vmCtx, newRes); err != nil {
				vmLogger.Error(err, "failed to create failover reservation", "reservationName", newRes.Name)
				vmFailed++
				break
			}

			newRes.Status = *savedStatus

			if err := c.patchReservationStatus(vmCtx, newRes); err != nil {
				vmLogger.V(1).Info("failed to update failover reservation status", "error", err, "reservationName", newRes.Name, "status", newRes.Status)
			} else {
				vmLogger.Info("successfully updated failover reservation status",
					"reservationName", newRes.Name,
					"host", newRes.Status.Host,
					"allocations", newRes.Status.FailoverReservation.Allocations)
			}

			vmCreated++
			failoverReservations = append(failoverReservations, *newRes)
		}

		vmLogger.Info("processed VM failover reservations",
			"flavorName", need.VM.FlavorName,
			"needed", need.Count,
			"reused", vmReused,
			"created", vmCreated,
			"failed", vmFailed)

		totalReused += vmReused
		totalCreated += vmCreated
		totalFailed += vmFailed
	}

	logger.Info("failover reservation assignment summary",
		"vmsProcessed", len(vmsMissingFailover),
		"reservationsNeeded", totalReservationsNeeded,
		"totalReused", totalReused,
		"totalCreated", totalCreated,
		"totalFailed", totalFailed)

	return nil, hitMaxVMsLimit
}

// calculateVMsMissingFailover calculates which VMs need failover reservations and how many.
func (c *FailoverReservationController) calculateVMsMissingFailover(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) []vmFailoverNeed {

	logger := LoggerFromContext(ctx)

	var result []vmFailoverNeed
	totalReservationsNeeded := 0

	for _, vm := range vms {
		requiredCount := c.getRequiredFailoverCount(vm.FlavorName)
		if requiredCount == 0 {
			continue
		}

		currentCount := countReservationsForVM(failoverReservations, vm.UUID)

		if currentCount >= requiredCount {
			continue
		}

		needed := requiredCount - currentCount
		totalReservationsNeeded += needed

		logger.V(2).Info("VM needs more failover reservations",
			"vmUUID", vm.UUID,
			"flavorName", vm.FlavorName,
			"currentCount", currentCount,
			"requiredCount", requiredCount,
			"needed", needed)

		result = append(result, vmFailoverNeed{
			VM:    vm,
			Count: needed,
		})
	}

	if len(result) > 0 {
		logger.Info("VMs missing failover reservations summary",
			"vmCount", len(result),
			"totalReservationsNeeded", totalReservationsNeeded)
	}

	return result
}

// getRequiredFailoverCount returns the number of failover reservations required for a flavor.
// It supports glob patterns in the configuration.
// If multiple patterns match, the pattern with the highest count is used.
func (c *FailoverReservationController) getRequiredFailoverCount(flavorName string) int {
	if flavorName == "" {
		return 0
	}

	maxCount := 0
	for pattern, count := range c.Config.FlavorFailoverRequirements {
		matched, err := filepath.Match(pattern, flavorName)
		if err != nil {
			log.Error(err, "invalid pattern in FlavorFailoverRequirements", "pattern", pattern)
			continue
		}
		if matched && count > maxCount {
			maxCount = count
		}
	}
	return maxCount
}

// patchReservationStatus patches the status of a reservation using MergeFrom.
func (c *FailoverReservationController) patchReservationStatus(ctx context.Context, res *v1alpha1.Reservation) error {
	logger := LoggerFromContext(ctx).WithValues("reservationName", res.Name)

	current := &v1alpha1.Reservation{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(res), current); err != nil {
		logger.Error(err, "failed to get current reservation state")
		return fmt.Errorf("failed to get current reservation state: %w", err)
	}

	old := current.DeepCopy()
	current.Status = res.Status

	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, current, patch); err != nil {
		logger.Error(err, "failed to patch reservation status")
		return fmt.Errorf("failed to patch reservation status: %w", err)
	}

	return nil
}

// ============================================================================
// Manager Setup
// ============================================================================

// SetupWithManager sets up the watch-based reconciler with the Manager.
// This handles per-reservation reconciliation triggered by CRD changes.
func (c *FailoverReservationController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	c.Recorder = mgr.GetEventRecorderFor("failover-reservation-controller")

	return multicluster.BuildController(mcl, mgr).
		For(&v1alpha1.Reservation{}).
		WithEventFilter(failoverReservationPredicate).
		Named("failover-reservation").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(c)
}

// Start implements manager.Runnable.
// It runs the periodic reconciliation loop at the configured interval.
// This can be called directly when the controller is created after the manager starts.
func (c *FailoverReservationController) Start(ctx context.Context) error {
	log.Info("starting failover reservation controller (periodic)",
		"reconcileInterval", c.Config.ReconcileInterval,
		"shortReconcileInterval", c.Config.ShortReconcileInterval,
		"creator", c.Config.Creator,
		"datasourceName", c.Config.DatasourceName,
		"schedulerURL", c.Config.SchedulerURL,
		"flavorFailoverRequirements", c.Config.FlavorFailoverRequirements,
		"maxVMsToProcess", c.Config.MaxVMsToProcess)

	timer := time.NewTimer(c.Config.ReconcileInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping failover reservation controller")
			return nil
		case <-timer.C:
			result, err := c.ReconcilePeriodic(ctx)
			if err != nil {
				log.Error(err, "failover reconciliation failed")
			}
			next := c.Config.ReconcileInterval
			if result.RequeueAfter > 0 {
				next = result.RequeueAfter
			}
			timer.Reset(next)
		}
	}
}

// failoverReservationPredicate filters to only process failover reservations.
var failoverReservationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		return ok && res.Spec.Type == v1alpha1.ReservationTypeFailover
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		res, ok := e.ObjectNew.(*v1alpha1.Reservation)
		return ok && res.Spec.Type == v1alpha1.ReservationTypeFailover
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	GenericFunc: func(e event.GenericEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		return ok && res.Spec.Type == v1alpha1.ReservationTypeFailover
	},
}

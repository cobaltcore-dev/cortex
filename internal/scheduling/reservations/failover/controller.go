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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	reconcileCount  int64 // Track reconciliation count for rotating VM selection
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
	logger := log.WithValues("reservation", req.Name, "namespace", req.Namespace)
	logger.V(1).Info("reconciling failover reservation")

	// Fetch the reservation
	var res v1alpha1.Reservation
	if err := c.Get(ctx, req.NamespacedName, &res); err != nil {
		if apierrors.IsNotFound(err) {
			// Resource was deleted - nothing to do
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
// If invalid, the reservation is deleted. On success, AcknowledgedAt is always updated.
func (c *FailoverReservationController) reconcileValidateAndAcknowledge(ctx context.Context, res *v1alpha1.Reservation) (ctrl.Result, error) {
	logger := log.WithValues("reservation", res.Name)
	logger.V(1).Info("validating failover reservation")

	// Validate the reservation
	valid := c.validateReservation(ctx, res)

	if !valid {
		// Reservation is invalid - delete it
		logger.Info("reservation validation failed, deleting",
			"host", res.Status.Host)
		if err := c.Delete(ctx, res); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			logger.Error(err, "failed to delete invalid reservation")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update AcknowledgedAt on successful validation
	updatedRes := res.DeepCopy()
	acknowledgedAt := metav1.Now()
	updatedRes.Status.FailoverReservation.AcknowledgedAt = &acknowledgedAt

	if err := c.patchReservationStatus(ctx, updatedRes); err != nil {
		logger.Error(err, "failed to update reservation acknowledgment")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("reservation validation passed",
		"host", res.Status.Host,
		"acknowledgedAt", acknowledgedAt)

	// Requeue for next re-validation
	return ctrl.Result{RequeueAfter: c.Config.RevalidationInterval}, nil
}

// validateReservation validates that a reservation is still valid for all its allocated VMs.
// Returns true if all VMs pass validation, false if any VM fails.
// TODO when should we invalidate a reservation? Currently we do that if we do not get a VM from Postgres or if the scheduler evacuation check fails. We may want to keep the reservation in those cases
func (c *FailoverReservationController) validateReservation(ctx context.Context, res *v1alpha1.Reservation) bool {
	allocations := getFailoverAllocations(res)
	if len(allocations) == 0 {
		// No VMs allocated - reservation is valid (will be cleaned up by periodic controller)
		return true
	}

	reservationHost := res.Status.Host
	if reservationHost == "" {
		log.Info("reservation has no host, marking as invalid",
			"reservationName", res.Name)
		return false
	}

	log.V(1).Info("validating reservation",
		"reservationName", res.Name,
		"host", reservationHost,
		"vmCount", len(allocations))

	// Validate each VM can still use the reservation host
	for vmUUID, vmCurrentHost := range allocations {
		// Get VM details
		vm, err := c.VMSource.GetVM(ctx, vmUUID)
		if err != nil {
			log.Error(err, "failed to get VM for validation",
				"reservationName", res.Name,
				"vmUUID", vmUUID)
			// Treat as validation failure - VM might not exist
			return false
		}
		if vm == nil {
			// VM not found - skip this VM (will be cleaned up by periodic controller)
			log.V(1).Info("VM not found during validation, skipping",
				"reservationName", res.Name,
				"vmUUID", vmUUID)
			continue
		}

		// Validate the VM can use the reservation host via scheduler evacuation
		valid, err := c.validateVMViaSchedulerEvacuation(ctx, *vm, reservationHost, vmCurrentHost)
		if err != nil {
			log.Error(err, "failed to validate VM for reservation host",
				"reservationName", res.Name,
				"vmUUID", vmUUID,
				"reservationHost", reservationHost)
			// Treat scheduler errors as validation failure
			return false
		}

		// TODO we just invalidate the entire reservation if one VM is not placable anymore
		// That is probably ok as most likely due to concurrency we just do not have space and then all VMs are affected
		// but it is also possible that it can be because of anti-affinity rules
		if !valid {
			log.Info("VM failed validation for reservation host",
				"reservationName", res.Name,
				"vmUUID", vmUUID,
				"vmCurrentHost", vmCurrentHost,
				"reservationHost", reservationHost)
			return false
		}

		log.V(1).Info("VM passed validation for reservation host",
			"reservationName", res.Name,
			"vmUUID", vmUUID,
			"reservationHost", reservationHost)
	}

	return true
}

// ============================================================================
// Periodic Bulk Reconciliation
// ============================================================================

// ReconcilePeriodic handles the periodic bulk reconciliation of all VMs and reservations.
// This ensures VMs have proper failover coverage by creating, reusing, and cleaning up reservations.
// TODO consider moving Step 3-5 (particularly) to the watch-based reconciliation
func (c *FailoverReservationController) ReconcilePeriodic(ctx context.Context) (ctrl.Result, error) {
	c.reconcileCount++
	log.Info("running periodic reconciliation", "reconcileCount", c.reconcileCount)

	// 1. Get hypervisors from the cluster
	var hypervisorList hv1.HypervisorList
	if err := c.List(ctx, &hypervisorList); err != nil {
		log.Error(err, "failed to list hypervisors")
		return ctrl.Result{}, err
	}

	allHypervisors := make([]string, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		allHypervisors = append(allHypervisors, hv.Name)
	}

	// 2. Get all VMs that might need failover reservations
	// This also handles filtering/enrichment based on TrustHypervisorLocation config
	vms, err := c.VMSource.ListVMsOnHypervisors(ctx, &hypervisorList, c.Config.TrustHypervisorLocation)
	if err != nil {
		log.Error(err, "failed to list VMs")
		return ctrl.Result{}, err
	}
	log.Info("found VMs from source", "count", len(vms))

	// List only failover reservations using label selector
	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList, client.MatchingLabels{
		"cortex.sap.com/type": "failover",
	}); err != nil {
		log.Error(err, "failed to list failover reservations")
		return ctrl.Result{}, err
	}
	failoverReservations := reservationList.Items
	log.Info("found failover reservations", "count", len(failoverReservations))

	// 3. Remove VMs from reservations if they are no longer valid (e.g. VM deleted or moved to different host)
	failoverReservations, reservationsToUpdate := reconcileRemoveInvalidVMFromReservations(vms, failoverReservations)

	// Persist the updated reservations to the cluster using patch for conflict-resistance
	for _, res := range reservationsToUpdate {
		if err := c.patchReservationStatus(ctx, res); err != nil {
			log.Error(err, "failed to update reservation after removing invalid VMs",
				"reservationName", res.Name)
			// Continue with other reservations even if one fails
		}
	}
	if len(reservationsToUpdate) > 0 {
		log.Info("updated reservations after removing invalid VMs", "count", len(reservationsToUpdate))
	}

	// 4. Remove VMs from reservations if they no longer meet eligibility criteria
	failoverReservations, nonEligibleReservationsToUpdate := reconcileRemoveNoneligibleVMFromReservations(vms, failoverReservations)

	// Persist the updated reservations to the cluster using patch for conflict-resistance
	for _, res := range nonEligibleReservationsToUpdate {
		if err := c.patchReservationStatus(ctx, res); err != nil {
			log.Error(err, "failed to update reservation after removing non-eligible VMs",
				"reservationName", res.Name)
			// Continue with other reservations even if one fails
		}
	}
	if len(nonEligibleReservationsToUpdate) > 0 {
		log.Info("updated reservations after removing non-eligible VMs", "count", len(nonEligibleReservationsToUpdate))
	}

	// 5. Remove empty failover reservations (no allocated VMs)
	failoverReservations, emptyReservationsToDelete := reconcileRemoveEmptyReservations(failoverReservations)

	// Delete empty reservations from the cluster
	for _, res := range emptyReservationsToDelete {
		if err := c.Delete(ctx, res); err != nil {
			log.Error(err, "failed to delete empty failover reservation",
				"reservationName", res.Name)
			// Continue with other reservations even if one fails
		} else {
			log.Info("deleted empty failover reservation",
				"reservationName", res.Name,
				"hypervisor", res.Status.Host)
		}
	}
	if len(emptyReservationsToDelete) > 0 {
		log.Info("deleted empty failover reservations", "count", len(emptyReservationsToDelete))
	}

	// 6. Create and assign reservations for VMs that need them
	err, hitMaxVMsLimit := c.reconcileCreateAndAssignReservations(ctx, vms, failoverReservations, allHypervisors)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info("completed periodic reconciliation")

	// If we hit the MaxVMsToProcess limit, requeue with a shorter interval to process remaining VMs faster
	if hitMaxVMsLimit && c.Config.ShortReconcileInterval > 0 {
		log.Info("requeuing with short interval due to MaxVMsToProcess limit",
			"shortReconcileInterval", c.Config.ShortReconcileInterval)
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
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToUpdate []*v1alpha1.Reservation) {

	// Build a map of VM UUID -> current hypervisor for quick lookup
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
				log.Info("removing VM from reservation allocations because VM no longer exists",
					"reservation", res.Name,
					"vmUUID", vmUUID)
				needsUpdate = true
				// Don't add to updatedAllocations - VM is removed
				continue
			}
			if vmCurrentHypervisor != allocatedHypervisor {
				log.Info("removing VM from reservation allocations because hypervisor has changed",
					"reservation", res.Name,
					"vmUUID", vmUUID,
					"allocatedHypervisor", allocatedHypervisor,
					"currentHypervisor", vmCurrentHypervisor)
				needsUpdate = true
				// Don't add to updatedAllocations - VM moved, needs re-evaluation
				continue
			}
			// VM is still valid - keep in allocations
			updatedAllocations[vmUUID] = allocatedHypervisor
		}

		if needsUpdate {
			// Create a copy of the reservation with updated allocations
			updatedRes := res.DeepCopy()
			if updatedRes.Status.FailoverReservation == nil {
				updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
			}
			updatedRes.Status.FailoverReservation.Allocations = updatedAllocations
			// Mark the reservation as changed and not yet acknowledged
			now := metav1.Now()
			updatedRes.Status.FailoverReservation.LastChanged = &now
			updatedRes.Status.FailoverReservation.AcknowledgedAt = nil
			updatedReservations = append(updatedReservations, *updatedRes)
			reservationsToUpdate = append(reservationsToUpdate, updatedRes)
		} else {
			// No changes needed - keep original reservation
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToUpdate
}

// reconcileRemoveNoneligibleVMFromReservations removes VMs from reservation allocations if
// they no longer meet eligibility criteria (checked via IsVMEligibleForReservation).
// Returns the updated list of reservations (with modifications applied in-memory).
// The caller is responsible for persisting any changes to the cluster.
func reconcileRemoveNoneligibleVMFromReservations(
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToUpdate []*v1alpha1.Reservation) {

	// Build a map of VM UUID -> VM for quick lookup
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
				// VM doesn't exist in our list - keep it (handled by reconcileRemoveInvalidVMFromReservations)
				updatedAllocations[vmUUID] = allocatedHypervisor
				continue
			}

			// Check if VM is still eligible for this reservation
			// We need to temporarily remove the VM from the reservation to check eligibility
			// because IsVMEligibleForReservation checks if the VM is already using the reservation
			tempRes := res.DeepCopy()
			delete(tempRes.Status.FailoverReservation.Allocations, vmUUID)

			// Build a temporary list of reservations without this VM in this reservation
			tempReservations := make([]v1alpha1.Reservation, 0, len(failoverReservations))
			for _, r := range failoverReservations {
				if r.Name == res.Name {
					tempReservations = append(tempReservations, *tempRes)
				} else {
					tempReservations = append(tempReservations, r)
				}
			}

			if !IsVMEligibleForReservation(vm, *tempRes, tempReservations) {
				log.Info("removing VM from reservation allocations because it no longer meets eligibility criteria",
					"reservation", res.Name,
					"vmUUID", vmUUID,
					"vmHypervisor", vm.CurrentHypervisor,
					"reservationHypervisor", res.Status.Host)
				needsUpdate = true
				// Don't add to updatedAllocations - VM is removed
				continue
			}
			// VM is still eligible - keep in allocations
			updatedAllocations[vmUUID] = allocatedHypervisor
		}

		if needsUpdate {
			// Create a copy of the reservation with updated allocations
			updatedRes := res.DeepCopy()
			if updatedRes.Status.FailoverReservation == nil {
				updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
			}
			updatedRes.Status.FailoverReservation.Allocations = updatedAllocations
			// Mark the reservation as changed and not yet acknowledged
			now := metav1.Now()
			updatedRes.Status.FailoverReservation.LastChanged = &now
			updatedRes.Status.FailoverReservation.AcknowledgedAt = nil
			updatedReservations = append(updatedReservations, *updatedRes)
			reservationsToUpdate = append(reservationsToUpdate, updatedRes)
		} else {
			// No changes needed - keep original reservation
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToUpdate
}

// reconcileRemoveEmptyReservations removes failover reservations that have no allocated VMs.
// Returns the updated list of reservations (without empty ones) and the reservations to delete.
func reconcileRemoveEmptyReservations(
	failoverReservations []v1alpha1.Reservation,
) (updatedReservations []v1alpha1.Reservation, reservationsToDelete []*v1alpha1.Reservation) {

	updatedReservations = make([]v1alpha1.Reservation, 0, len(failoverReservations))

	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		if len(allocations) == 0 {
			// Reservation has no allocated VMs - mark for deletion
			resCopy := res.DeepCopy()
			reservationsToDelete = append(reservationsToDelete, resCopy)
			log.Info("marking empty failover reservation for deletion",
				"reservationName", res.Name,
				"hypervisor", res.Status.Host)
		} else {
			// Reservation still has VMs - keep it
			updatedReservations = append(updatedReservations, res)
		}
	}

	return updatedReservations, reservationsToDelete
}

// selectVMsToProcess selects a subset of VMs to process based on MaxVMsToProcess limit.
// VMs are sorted by memory (largest first) to prioritize large VMs for failover reservations.
// 3 out of 4 reconciliations start at offset 0 (process largest VMs first).
// Every 4th reconciliation uses a rotating offset to try different VMs
// if the largest VMs consistently fail to get reservations.
func (c *FailoverReservationController) selectVMsToProcess(
	vmsMissingFailover []vmFailoverNeed,
	maxToProcess int,
) (selected []vmFailoverNeed, hitLimit bool) {

	if len(vmsMissingFailover) == 0 {
		return vmsMissingFailover, false
	}

	// Sort by memory (largest first) to prioritize large VMs
	sortVMsByMemory(vmsMissingFailover)

	if maxToProcess <= 0 || len(vmsMissingFailover) <= maxToProcess {
		return vmsMissingFailover, false
	}

	// 3 out of 4 runs start at offset 0, every 4th run uses reconcileCount as offset
	offset := 0
	if c.reconcileCount%4 == 0 {
		// Every 4th reconciliation, use reconcileCount as offset (mod vmCount to wrap around)
		offset = int(c.reconcileCount) % len(vmsMissingFailover)
	}

	// Select VMs starting from offset, wrapping around
	selected = make([]vmFailoverNeed, 0, maxToProcess)
	for i := range maxToProcess {
		idx := (offset + i) % len(vmsMissingFailover)
		selected = append(selected, vmsMissingFailover[idx])
	}

	log.Info("selected VMs to process (sorted by memory, with rotation)",
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
// Note: This function logs errors but continues processing to handle as many VMs as possible.
// Returns:
//   - error: always nil (errors are logged but processing continues)
//   - hitMaxVMsLimit: true if MaxVMsToProcess limit was hit and there are more VMs to process
//
//nolint:unparam // error return is intentionally always nil - we log errors but continue processing
func (c *FailoverReservationController) reconcileCreateAndAssignReservations(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
	allHypervisors []string,
) (error, bool) {
	// Calculate list of all VMs that are missing failover reservations
	vmsMissingFailover := c.calculateVMsMissingFailover(vms, failoverReservations)
	log.Info("VMs missing failover reservations", "count", len(vmsMissingFailover))

	// Apply MaxVMsToProcess limit if configured, using rotating selection
	vmsMissingFailover, hitMaxVMsLimit := c.selectVMsToProcess(vmsMissingFailover, c.Config.MaxVMsToProcess)

	log.Info("found hypervisors and vm missing failover reservation", "countHypervisors", len(allHypervisors), "countVMsMissingFailover", len(vmsMissingFailover))

	// Calculate total reservations needed
	totalReservationsNeeded := 0
	for _, need := range vmsMissingFailover {
		totalReservationsNeeded += need.Count
	}

	// Track statistics for summary
	var totalReused, totalCreated, totalFailed int

	// For each VM missing failover, try to reuse existing reservations or create new ones
	for _, need := range vmsMissingFailover {
		vmReused := 0
		vmCreated := 0
		vmFailed := 0

		for i := range need.Count {
			// First, try to find and reuse an existing reservation
			reusedRes := c.tryReuseExistingReservation(
				ctx, need.VM, failoverReservations, allHypervisors,
			)

			if reusedRes != nil {
				// Persist the updated reservation to the cluster using patch for conflict-resistance
				if err := c.patchReservationStatus(ctx, reusedRes); err != nil {
					log.Error(err, "failed to persist reused reservation",
						"vmUUID", need.VM.UUID,
						"reservationName", reusedRes.Name)
					vmFailed++
					continue
				}
				vmReused++
				// Update the reservation in our local list so following VMs see the updated UsedBy
				for j := range failoverReservations {
					if failoverReservations[j].Name == reusedRes.Name {
						failoverReservations[j] = *reusedRes
						break
					}
				}
				continue
			}

			// If no reusable reservation found, build and create a new one
			newRes, err := c.scheduleAndBuildNewFailoverReservation(ctx, need.VM, allHypervisors, failoverReservations)
			if err != nil {
				log.V(1).Info("failed to schedule failover reservation",
					"error", err,
					"vmUUID", need.VM.UUID,
					"iteration", i+1,
					"needed", need.Count)
				vmFailed++
				// Continue with other VMs even if one fails
				break // Skip remaining iterations for this VM
			}

			// Save the status before Create (Create doesn't persist status subresource)
			savedStatus := newRes.Status.DeepCopy()

			// Persist the new reservation to the cluster (spec only, status is a subresource)
			if err := c.Create(ctx, newRes); err != nil {
				log.Error(err, "failed to create failover reservation",
					"vmUUID", need.VM.UUID,
					"reservationName", newRes.Name)
				vmFailed++
				break // Skip remaining iterations for this VM
			}

			// Re-apply the saved status (Create response overwrites in-memory status)
			newRes.Status = *savedStatus

			// Update the status subresource using patch for conflict-resistance
			if err := c.patchReservationStatus(ctx, newRes); err != nil {
				log.V(1).Info("failed to update failover reservation status",
					"error", err,
					"vmUUID", need.VM.UUID,
					"reservationName", newRes.Name,
					"status", newRes.Status)
				// Continue anyway - the reservation was created
			} else {
				log.Info("successfully updated failover reservation status",
					"reservationName", newRes.Name,
					"host", newRes.Status.Host,
					"allocations", newRes.Status.FailoverReservation.Allocations)
			}

			vmCreated++

			// Add the new reservation to our local list so following VMs can consider it
			failoverReservations = append(failoverReservations, *newRes)
		}

		// Log outcome for this VM
		log.Info("processed VM failover reservations",
			"vmUUID", need.VM.UUID,
			"flavorName", need.VM.FlavorName,
			"needed", need.Count,
			"reused", vmReused,
			"created", vmCreated,
			"failed", vmFailed)

		totalReused += vmReused
		totalCreated += vmCreated
		totalFailed += vmFailed
	}

	// Log summary
	log.Info("failover reservation assignment summary",
		"vmsProcessed", len(vmsMissingFailover),
		"reservationsNeeded", totalReservationsNeeded,
		"totalReused", totalReused,
		"totalCreated", totalCreated,
		"totalFailed", totalFailed)

	return nil, hitMaxVMsLimit
}

// calculateVMsMissingFailover calculates which VMs need failover reservations and how many.
func (c *FailoverReservationController) calculateVMsMissingFailover(
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) []vmFailoverNeed {

	var result []vmFailoverNeed
	totalReservationsNeeded := 0

	for _, vm := range vms {
		requiredCount := c.getRequiredFailoverCount(vm.FlavorName)
		if requiredCount == 0 {
			continue // This VM doesn't need failover reservations
		}

		// Count how many failover reservations this VM is already in
		currentCount := countReservationsForVM(failoverReservations, vm.UUID)

		if currentCount >= requiredCount {
			continue // VM has enough failover coverage
		}

		needed := requiredCount - currentCount
		totalReservationsNeeded += needed

		// Detailed per-VM logging at verbosity level 2
		log.V(2).Info("VM needs more failover reservations",
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

	// Summary log at normal verbosity
	if len(result) > 0 {
		log.Info("VMs missing failover reservations summary",
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
		return 0 // Can't determine requirements without flavor name
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
// This is more conflict-resistant than Update() as it only sends the diff.
func (c *FailoverReservationController) patchReservationStatus(ctx context.Context, res *v1alpha1.Reservation) error {
	// Get the current state from the cluster to use as the base for the patch
	current := &v1alpha1.Reservation{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(res), current); err != nil {
		return fmt.Errorf("failed to get current reservation state: %w", err)
	}

	// Create a copy of the current state to use as the base for MergeFrom
	old := current.DeepCopy()

	// Apply the status changes from res to current
	current.Status = res.Status

	// Create and apply the patch
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, current, patch); err != nil {
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
	return multicluster.BuildController(mcl, mgr).
		For(&v1alpha1.Reservation{}).
		WithEventFilter(failoverReservationPredicate).
		Named("failover-reservation").
		WithOptions(controller.Options{
			// Process reservations one at a time to avoid race conditions
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
		"creator", c.Config.Creator,
		"datasourceName", c.Config.DatasourceName,
		"schedulerURL", c.Config.SchedulerURL,
		"flavorFailoverRequirements", c.Config.FlavorFailoverRequirements,
		"maxVMsToProcess", c.Config.MaxVMsToProcess)

	// Set up periodic reconciliation
	ticker := time.NewTicker(c.Config.ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping failover reservation controller")
			return nil
		case <-ticker.C:
			if _, err := c.ReconcilePeriodic(ctx); err != nil {
				log.Error(err, "failover reconciliation failed")
				// Continue with next iteration even if this one failed
			}
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
		// We don't need to reconcile on delete - the resource is gone
		return false
	},
	GenericFunc: func(e event.GenericEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		return ok && res.Spec.Type == v1alpha1.ReservationTypeFailover
	},
}

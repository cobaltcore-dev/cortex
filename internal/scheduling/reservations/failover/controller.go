// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("failover-reservation-controller").WithValues("module", "reservations/failover")

// FailoverReservationController manages failover reservations for VMs.
// It ensures that VMs with matching flavors have sufficient failover reservations.
type FailoverReservationController struct {
	client.Client
	VMSource        VMSource
	Config          FailoverConfig
	SchedulerClient *reservations.SchedulerClient
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

// Reconcile ensures all VMs have sufficient failover reservations.
// This is called periodically based on Config.ReconcileInterval.
func (c *FailoverReservationController) Reconcile(ctx context.Context) (ctrl.Result, error) {
	log.Info("starting failover reservation reconciliation")

	// 1. Get all VMs that might need failover reservations
	//fixme: this currently goes against postgres and needs to be cleanup in the future
	// the postgres is a caches the servers call to nova
	vms, err := c.VMSource.ListVMs(ctx)
	if err != nil {
		log.Error(err, "failed to list VMs")
		return ctrl.Result{}, err
	}
	log.Info("found VMs from source", "count", len(vms))

	// nova servers and the vms(servers) on hypervisors are potentially not in sync
	// we can only handle VMs we have in both
	var hypervisorList hv1.HypervisorList
	if err := c.List(ctx, &hypervisorList); err != nil {
		log.Error(err, "failed to list hypervisors")
		return ctrl.Result{}, err
	}

	allHypervisors := make([]string, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		allHypervisors = append(allHypervisors, hv.Name)
	}

	// Warn about VMs on hypervisors that are not in ListVMs (possible data sync issue)
	warnUnknownVMsOnHypervisors(&hypervisorList, vms)

	vms = filterVMsOnKnownHypervisors(vms, allHypervisors)
	log.Info("filtered VMs to those on known hypervisors", "count", len(vms), "knownHypervisors", len(allHypervisors))

	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList); err != nil {
		log.Error(err, "failed to list reservations")
		return ctrl.Result{}, err
	}

	// Filter to only failover reservations
	failoverReservations := filterFailoverReservations(reservationList.Items)
	log.Info("found failover reservations", "count", len(failoverReservations))

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
	failoverReservations, nonEligibleReservationsToUpdate := reconcileRemoveNoneligibleVMFromReservations(vms, failoverReservations, c.Config.MaxVMsToProcess)

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
	if err := c.reconcileCreateAndAssignReservations(ctx, vms, failoverReservations); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("completed failover reservation reconciliation")
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
// If maxVMsToProcess > 0, processing stops after fixing that many VMs.
// Returns the updated list of reservations (with modifications applied in-memory).
// The caller is responsible for persisting any changes to the cluster.
func reconcileRemoveNoneligibleVMFromReservations(
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
	maxVMsToProcess int,
) (updatedReservations []v1alpha1.Reservation, reservationsToUpdate []*v1alpha1.Reservation) {

	// Build a map of VM UUID -> VM for quick lookup
	vmByUUID := make(map[string]VM)
	for _, vm := range vms {
		vmByUUID[vm.UUID] = vm
	}

	// Track how many VMs we've processed (removed from reservations)
	vmsProcessed := 0

	updatedReservations = make([]v1alpha1.Reservation, 0, len(failoverReservations))

	for _, res := range failoverReservations {
		// Check if we've hit the processing limit
		if maxVMsToProcess > 0 && vmsProcessed >= maxVMsToProcess {
			// Keep remaining reservations unchanged
			updatedReservations = append(updatedReservations, res)
			continue
		}

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
				vmsProcessed++

				// Check if we've hit the processing limit
				if maxVMsToProcess > 0 && vmsProcessed >= maxVMsToProcess {
					log.Info("reached MaxVMsToProcess limit during non-eligibility check, stopping",
						"maxVMsToProcess", maxVMsToProcess,
						"vmsProcessed", vmsProcessed)
					// Don't add this VM to updatedAllocations, but stop processing more VMs
					// Add remaining VMs in this reservation
					for remainingVMUUID, remainingHypervisor := range allocations {
						if remainingVMUUID != vmUUID && updatedAllocations[remainingVMUUID] == "" {
							updatedAllocations[remainingVMUUID] = remainingHypervisor
						}
					}
					break
				}
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

// reconcileCreateAndAssignReservations creates and assigns failover reservations for VMs that need them.
func (c *FailoverReservationController) reconcileCreateAndAssignReservations(
	ctx context.Context,
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) error {
	// Calculate list of all VMs that are missing failover reservations
	vmsMissingFailover := c.calculateVMsMissingFailover(vms, failoverReservations)
	log.Info("VMs missing failover reservations", "count", len(vmsMissingFailover))

	// Apply MaxVMsToProcess limit if configured (for debugging)
	if c.Config.MaxVMsToProcess > 0 && len(vmsMissingFailover) > c.Config.MaxVMsToProcess {
		log.Info("limiting VMs to process (MaxVMsToProcess configured)",
			"totalVMsMissingFailover", len(vmsMissingFailover),
			"maxVMsToProcess", c.Config.MaxVMsToProcess)
		vmsMissingFailover = vmsMissingFailover[:c.Config.MaxVMsToProcess]
	}

	// Get all available hypervisors for scheduling
	allHypervisors, err := c.getAllHypervisors(ctx)
	if err != nil {
		log.Error(err, "failed to get all hypervisors")
		return err
	}
	log.Info("found hypervisors and vm missing failover reservation", "countHypervisors", len(allHypervisors), "countVMsMissingFailover", len(vmsMissingFailover))

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
				// TODO after GA: it is possible (probably unlikely) that the host is not valid any more
				// mainly do to anti affinity an the possibility that between checking valid host and now a VM is placed here
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
				log.Error(err, "failed to schedule failover reservation",
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
				log.Error(err, "failed to update failover reservation status",
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
		"totalReused", totalReused,
		"totalCreated", totalCreated,
		"totalFailed", totalFailed)

	return nil
}

// calculateVMsMissingFailover calculates which VMs need failover reservations and how many.
func (c *FailoverReservationController) calculateVMsMissingFailover(
	vms []VM,
	failoverReservations []v1alpha1.Reservation,
) []vmFailoverNeed {

	var result []vmFailoverNeed

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
		log.Info("VM needs more failover reservations",
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

	return result
}

// getAllHypervisors returns all available hypervisor names.
func (c *FailoverReservationController) getAllHypervisors(ctx context.Context) ([]string, error) {
	hypervisorList, err := c.getHypervisors(ctx)
	if err != nil {
		return nil, err
	}

	hypervisors := make([]string, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		hypervisors = append(hypervisors, hv.Name)
	}

	return hypervisors, nil
}

// getHypervisors returns all hypervisors.
func (c *FailoverReservationController) getHypervisors(ctx context.Context) (*hv1.HypervisorList, error) {
	var hypervisorList hv1.HypervisorList
	if err := c.List(ctx, &hypervisorList); err != nil {
		return nil, fmt.Errorf("failed to list hypervisors: %w", err)
	}
	return &hypervisorList, nil
}

// warnUnknownVMsOnHypervisors logs a warning for VMs that are on hypervisors but not in the ListVMs (i.e. nova) result.
// This can indicate a data sync issue between the hypervisor operator and the VM datasource.
func warnUnknownVMsOnHypervisors(hypervisors *hv1.HypervisorList, vms []VM) {
	// Build a set of VM UUIDs from ListVMs
	vmUUIDs := make(map[string]bool, len(vms))
	for _, vm := range vms {
		vmUUIDs[vm.UUID] = true
	}

	// Build a set of VM UUIDs from hypervisors
	hypervisorVMUUIDs := make(map[string]bool)
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				hypervisorVMUUIDs[inst.ID] = true
			}
		}
	}

	// Check each hypervisor's instances - VMs on hypervisors but not in ListVMs
	vmsOnHypervisorsNotInListVMs := 0
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active && !vmUUIDs[inst.ID] {
				log.Info("WARNING: VM on hypervisor not found in ListVMs - possible data sync issue",
					"vmUUID", inst.ID,
					"vmName", inst.Name,
					"hypervisor", hv.Name)
				vmsOnHypervisorsNotInListVMs++
			}
		}
	}

	// Check VMs in ListVMs but not on any hypervisor
	vmsInListVMsNotOnHypervisors := 0
	for _, vm := range vms {
		if !hypervisorVMUUIDs[vm.UUID] {
			log.Info("WARNING: VM in ListVMs not found on any hypervisor - possible data sync issue",
				"vmUUID", vm.UUID,
				"vmCurrentHypervisor", vm.CurrentHypervisor)
			vmsInListVMsNotOnHypervisors++
		}
	}

	if vmsOnHypervisorsNotInListVMs > 0 {
		log.Info("WARNING: VMs on hypervisors not found in ListVMs",
			"count", vmsOnHypervisorsNotInListVMs,
			"hint", "This may indicate a data sync issue between hypervisor operator and nova servers")
	}

	if vmsInListVMsNotOnHypervisors > 0 {
		log.Info("WARNING: VMs in ListVMs not found on any hypervisor",
			"count", vmsInListVMsNotOnHypervisors,
			"hint", "This may indicate a data sync issue between nova servers and hypervisor operator")
	}
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

// filterFailoverReservations filters a list of reservations to only include failover reservations.
func filterFailoverReservations(resList []v1alpha1.Reservation) []v1alpha1.Reservation {
	var result []v1alpha1.Reservation
	for _, res := range resList {
		if res.Spec.Type == v1alpha1.ReservationTypeFailover {
			result = append(result, res)
		}
	}
	return result
}

// filterVMsOnKnownHypervisors filters VMs to only include those running on known hypervisors.
// This removes VMs that are on hypervisors not managed by the hypervisor operator.
func filterVMsOnKnownHypervisors(vms []VM, knownHypervisors []string) []VM {
	// Build a set of known hypervisors for O(1) lookup
	hypervisorSet := make(map[string]bool, len(knownHypervisors))
	for _, hypervisor := range knownHypervisors {
		hypervisorSet[hypervisor] = true
	}

	var result []VM
	for _, vm := range vms {
		if hypervisorSet[vm.CurrentHypervisor] {
			result = append(result, vm)
		}
	}
	return result
}

// countReservationsForVM counts how many reservations a VM is in.
func countReservationsForVM(resList []v1alpha1.Reservation, vmUUID string) int {
	count := 0
	for _, res := range resList {
		allocations := getFailoverAllocations(&res)
		if _, exists := allocations[vmUUID]; exists {
			count++
		}
	}
	return count
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

// SetupWithManager sets up the controller with the Manager.
// It registers a periodic reconciliation loop that runs at the configured interval.
func (c *FailoverReservationController) SetupWithManager(mgr ctrl.Manager) error {
	// Add a runnable that performs periodic reconciliation
	return mgr.Add(&failoverReconcileRunner{controller: c})
}

// Start implements manager.Runnable.
// It runs the reconciliation loop at the configured interval.
// This can be called directly when the controller is created after the manager starts.
func (c *FailoverReservationController) Start(ctx context.Context) error {
	runner := &failoverReconcileRunner{controller: c}
	return runner.Start(ctx)
}

// failoverReconcileRunner implements manager.Runnable for periodic reconciliation.
type failoverReconcileRunner struct {
	controller *FailoverReservationController
}

// Start implements manager.Runnable.
// It runs the reconciliation loop at the configured interval.
func (r *failoverReconcileRunner) Start(ctx context.Context) error {
	log.Info("starting failover reservation controller",
		"reconcileInterval", r.controller.Config.ReconcileInterval,
		"creator", r.controller.Config.Creator,
		"datasourceName", r.controller.Config.DatasourceName,
		"schedulerURL", r.controller.Config.SchedulerURL,
		"flavorFailoverRequirements", r.controller.Config.FlavorFailoverRequirements,
		"maxVMsToProcess", r.controller.Config.MaxVMsToProcess)

	// Run initial reconciliation
	if _, err := r.controller.Reconcile(ctx); err != nil {
		log.Error(err, "initial failover reconciliation failed")
		// Don't return error - continue with periodic reconciliation
	}

	// Set up periodic reconciliation
	ticker := time.NewTicker(r.controller.Config.ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping failover reservation controller")
			return nil
		case <-ticker.C:
			if _, err := r.controller.Reconcile(ctx); err != nil {
				log.Error(err, "failover reconciliation failed")
				// Continue with next iteration even if this one failed
			}
		}
	}
}

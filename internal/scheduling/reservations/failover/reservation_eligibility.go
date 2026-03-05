// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ============================================================================
// Data Structure Builders
// ============================================================================

// buildEligibilityData builds the data structures needed for eligibility checking.
// The VM is treated as already being part of the candidate reservation.
// Returns:
//   - vmToReservations: vm_uuid -> set of reservation names the VM uses
//   - vmToCurrentHypervisor: vm_uuid -> current hypervisor (where VM is now)
//   - vmToReservationHosts: vm_uuid -> set of reservation hosts (where VM could evacuate, NOT including current hypervisor)
//   - reservationToVMs: res-name -> set of vm_uuids using it
//   - reservationToHost: res-name -> hypervisor where reservation is placed
func buildEligibilityData(
	vm VM,
	candidateReservation v1alpha1.Reservation,
	allFailoverReservations []v1alpha1.Reservation,
) (
	vmToReservations map[string]map[string]bool,
	vmToCurrentHypervisor map[string]string,
	vmToReservationHosts map[string]map[string]bool,
	reservationToVMs map[string]map[string]bool,
	reservationToHost map[string]string,
) {

	vmToReservations = make(map[string]map[string]bool)
	vmToCurrentHypervisor = make(map[string]string)
	vmToReservationHosts = make(map[string]map[string]bool)
	reservationToVMs = make(map[string]map[string]bool)
	reservationToHost = make(map[string]string)

	// Helper to ensure maps are initialized
	ensureVMInMaps := func(vmUUID string) {
		if vmToReservations[vmUUID] == nil {
			vmToReservations[vmUUID] = make(map[string]bool)
		}
		if vmToReservationHosts[vmUUID] == nil {
			vmToReservationHosts[vmUUID] = make(map[string]bool)
		}
	}

	ensureResInMaps := func(resName string) {
		if reservationToVMs[resName] == nil {
			reservationToVMs[resName] = make(map[string]bool)
		}
	}

	// Process all reservations
	for _, res := range allFailoverReservations {
		resName := res.Name
		resHost := res.Status.Host

		ensureResInMaps(resName)
		reservationToHost[resName] = resHost

		allocations := getFailoverAllocations(&res)
		for vmUUID, vmHypervisor := range allocations {
			ensureVMInMaps(vmUUID)
			vmToReservations[vmUUID][resName] = true
			vmToCurrentHypervisor[vmUUID] = vmHypervisor
			vmToReservationHosts[vmUUID][resHost] = true // Only reservation host, NOT current hypervisor
			reservationToVMs[resName][vmUUID] = true
		}
	}

	// Process candidate reservation (may not be in allFailoverReservations)
	candidateResName := candidateReservation.Name
	candidateResHost := candidateReservation.Status.Host

	ensureResInMaps(candidateResName)
	reservationToHost[candidateResName] = candidateResHost

	candidateAllocations := getFailoverAllocations(&candidateReservation)
	for vmUUID, vmHypervisor := range candidateAllocations {
		ensureVMInMaps(vmUUID)
		vmToReservations[vmUUID][candidateResName] = true
		vmToCurrentHypervisor[vmUUID] = vmHypervisor
		vmToReservationHosts[vmUUID][candidateResHost] = true
		reservationToVMs[candidateResName][vmUUID] = true
	}

	// Add the VM we're checking with its current hypervisor
	ensureVMInMaps(vm.UUID)
	vmToCurrentHypervisor[vm.UUID] = vm.CurrentHypervisor

	// KEY: Treat VM as already in the candidate reservation
	vmToReservations[vm.UUID][candidateResName] = true
	vmToReservationHosts[vm.UUID][candidateResHost] = true // VM could evacuate to candidate reservation host
	reservationToVMs[candidateResName][vm.UUID] = true

	return vmToReservations, vmToCurrentHypervisor, vmToReservationHosts, reservationToVMs, reservationToHost
}

// ============================================================================
// Private Constraint Checking (uses pre-computed data structures)
// ============================================================================

// checkAllVMConstraints checks if a single VM satisfies all constraints (1-5).
// This function is called for each VM in the candidate reservation.
// Returns true if the VM satisfies all constraints.
func checkAllVMConstraints(
	vmUUID string,
	vmCurrentHypervisor string,
	vmToReservations map[string]map[string]bool,
	vmToCurrentHypervisor map[string]string,
	vmToReservationHosts map[string]map[string]bool,
	reservationToVMs map[string]map[string]bool,
) bool {
	// Get VM's slot hypervisors (all reservation hosts)
	vmSlotHypervisors := vmToReservationHosts[vmUUID]

	// Constraint (1): A VM cannot reserve a slot on its own hypervisor
	// Check if any of the VM's reservation hosts is the same as its current hypervisor
	if vmSlotHypervisors[vmCurrentHypervisor] {
		return false
	}

	// Constraint (2): A VM's N reservation slots must be on N distinct hypervisors
	// Check for duplicate hosts in vmToReservationHosts
	numReservations := len(vmToReservations[vmUUID])
	numUniqueHosts := len(vmToReservationHosts[vmUUID])
	if numUniqueHosts < numReservations {
		// Duplicate host - VM has multiple reservations on the same host
		return false
	}

	// Collect all VMs (other than this VM) that use any of this VM's slots
	// Track how many of this VM's slots each other VM uses
	vmsUsingVMSlotsCount := make(map[string]int)
	for resName := range vmToReservations[vmUUID] {
		for otherVM := range reservationToVMs[resName] {
			if otherVM != vmUUID {
				vmsUsingVMSlotsCount[otherVM]++
			}
		}
	}

	// Constraint (4): Any other VM vi that uses any of this VM's slots must not run on:
	// - this VM's current hypervisor
	// - any of this VM's slot hypervisors
	for otherVM := range vmsUsingVMSlotsCount {
		otherVMCurrentHypervisor := vmToCurrentHypervisor[otherVM]

		// Check if they run on this VM's hypervisor
		if otherVMCurrentHypervisor == vmCurrentHypervisor {
			return false
		}

		// Check if they run on any of this VM's slot hypervisors
		if vmSlotHypervisors[otherVMCurrentHypervisor] {
			return false
		}
	}

	// Constraint (5): No two VMs (other than this VM) using this VM's slots can be on the same hypervisor
	// Note: vm_j and vm_k CAN be the same VM (same VM using multiple slots violates this)
	hypervisorToVM := make(map[string]string)
	for otherVM, slotCount := range vmsUsingVMSlotsCount {
		// If a VM uses more than one of this VM's slots, it violates the constraint
		if slotCount > 1 {
			return false
		}

		// Check if any two different VMs using this VM's slots run on the same hypervisor
		otherVMCurrentHypervisor := vmToCurrentHypervisor[otherVM]
		if existingVM, exists := hypervisorToVM[otherVMCurrentHypervisor]; exists && existingVM != otherVM {
			// Two different VMs using this VM's slots run on the same hypervisor
			return false
		}
		hypervisorToVM[otherVMCurrentHypervisor] = otherVM
	}

	return true
}

// isVMEligibleForReservation checks if a VM is eligible for a reservation using pre-computed data.
// The VM is already treated as being in the candidate reservation in the data structures.
// This function checks all constraints for all VMs in the candidate reservation.
func isVMEligibleForReservation(
	candidateResName string,
	vmToReservations map[string]map[string]bool,
	vmToCurrentHypervisor map[string]string,
	vmToReservationHosts map[string]map[string]bool,
	reservationToVMs map[string]map[string]bool,
) bool {
	// Check all constraints (1-5) for ALL VMs in the candidate reservation
	for vmUUID := range reservationToVMs[candidateResName] {
		vmCurrentHypervisor := vmToCurrentHypervisor[vmUUID]
		if !checkAllVMConstraints(vmUUID, vmCurrentHypervisor, vmToReservations, vmToCurrentHypervisor, vmToReservationHosts, reservationToVMs) {
			return false
		}
	}

	// All constraints passed for all VMs
	return true
}

// ============================================================================
// Public API
// ============================================================================

// IsVMEligibleForReservation checks if a VM is eligible to use a specific reservation.
// A VM is eligible if it satisfies all the following constraints:
// (1) A VM cannot reserve a slot on its own hypervisor.
// (2) A VM's N reservation slots must be placed on N distinct hypervisors (no hypervisor overlap among slots).
// (3) For any reservation r, no two VMs that use r may be on the same hypervisor (directly or potentially via a reservation).
// (4) For VM v with slots R = {r1..rn}, any other VM vi that uses any rj must not run on hs(v) nor on any hs(rj).
// (5) For VM v with slots R = {r1..rn}, there exist no vm_1, vm_2 (vm_1 != v and vm_2 != v)
//
//	with vm_1 uses r_j and vm_2 uses r_k and hypervisor(vm_1) = hypervisor(vm_2).
func IsVMEligibleForReservation(vm VM, reservation v1alpha1.Reservation, allFailoverReservations []v1alpha1.Reservation) bool {
	// Check if VM is already using this reservation
	resAllocations := getFailoverAllocations(&reservation)
	if _, exists := resAllocations[vm.UUID]; exists {
		return false
	}

	// Ensure the candidate reservation is included in allFailoverReservations
	reservationInList := false
	for _, res := range allFailoverReservations {
		if res.Name == reservation.Name && res.Namespace == reservation.Namespace {
			reservationInList = true
			break
		}
	}
	if !reservationInList {
		allFailoverReservations = append(append([]v1alpha1.Reservation{}, allFailoverReservations...), reservation)
	}

	// Build data structures (with VM already in the candidate reservation)
	vmToReservations, vmToCurrentHypervisor, vmToReservationHosts, reservationToVMs, _ := buildEligibilityData(
		vm, reservation, allFailoverReservations,
	)

	// Delegate to private function - check all constraints for all VMs in the reservation
	return isVMEligibleForReservation(
		reservation.Name,
		vmToReservations,
		vmToCurrentHypervisor,
		vmToReservationHosts,
		reservationToVMs,
	)
}

// ============================================================================
// Resource Fit Check
// ============================================================================

// DoesVMFitInReservation checks if a VM's resources fit within a reservation's resources.
// Returns true if all VM resources are less than or equal to the reservation's resources.
func DoesVMFitInReservation(vm VM, reservation v1alpha1.Reservation) bool {
	// Check memory: VM's memory must be <= reservation's memory
	if vmMemory, ok := vm.Resources["memory"]; ok {
		if resMemory, ok := reservation.Spec.Resources["memory"]; ok {
			if vmMemory.Cmp(resMemory) > 0 {
				return false // VM memory exceeds reservation memory
			}
		} else {
			return false // Reservation has no memory resource defined
		}
	}

	// Check CPU: VM's vcpus must be <= reservation's vcpus
	// Note: Both VM and Reservation use "vcpus" as the resource key
	if vmVCPUs, ok := vm.Resources["vcpus"]; ok {
		if resVCPUs, ok := reservation.Spec.Resources["vcpus"]; ok {
			if vmVCPUs.Cmp(resVCPUs) > 0 {
				return false // VM vcpus exceeds reservation vcpus
			}
		} else {
			return false // Reservation has no vcpus resource defined
		}
	}

	return true
}

// ============================================================================
// Finding Eligible Reservations
// ============================================================================

// FindEligibleReservations finds all reservations that a VM is eligible to use.
// It checks both resource fit (DoesVMFitInReservation) and eligibility constraints (IsVMEligibleForReservation).
func FindEligibleReservations(
	vm VM,
	failoverReservations []v1alpha1.Reservation,
) []v1alpha1.Reservation {

	//TODO: we create data mappings inside IsVMEligibleForReservation those should probably be done already on this level to avoid redundant work
	var eligible []v1alpha1.Reservation
	for _, res := range failoverReservations {
		if DoesVMFitInReservation(vm, res) && IsVMEligibleForReservation(vm, res, failoverReservations) {
			eligible = append(eligible, res)
		}
	}

	return eligible
}

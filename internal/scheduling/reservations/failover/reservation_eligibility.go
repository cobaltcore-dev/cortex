// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ============================================================================
// Helper Functions
// ============================================================================

// buildVMHypervisorsMap builds a map of VM UUID to their hypervisors from failover reservations.
// It also includes the VM we are checking (vm) with its current hypervisor,
// and the candidate reservation (which may have VMs not in allFailoverReservations).
func buildVMHypervisorsMap(vm VM, candidateReservation v1alpha1.Reservation, allFailoverReservations []v1alpha1.Reservation) map[string]map[string]bool {
	vmHypervisorsMap := make(map[string]map[string]bool)

	// Add the VM we are checking
	if vmHypervisorsMap[vm.UUID] == nil {
		vmHypervisorsMap[vm.UUID] = make(map[string]bool)
	}
	vmHypervisorsMap[vm.UUID][vm.CurrentHypervisor] = true

	// Add VMs from reservation allocations
	for _, res := range allFailoverReservations {
		allocations := getFailoverAllocations(&res)
		for vmUUID, vmHypervisor := range allocations {
			if vmHypervisorsMap[vmUUID] == nil {
				vmHypervisorsMap[vmUUID] = make(map[string]bool)
			}
			vmHypervisorsMap[vmUUID][vmHypervisor] = true
		}
	}

	// Add VMs from the candidate reservation
	candidateAllocations := getFailoverAllocations(&candidateReservation)
	for vmUUID, vmHypervisor := range candidateAllocations {
		if vmHypervisorsMap[vmUUID] == nil {
			vmHypervisorsMap[vmUUID] = make(map[string]bool)
		}
		vmHypervisorsMap[vmUUID][vmHypervisor] = true
	}

	return vmHypervisorsMap
}

// getVMExistingReservationHypervisors returns the hypervisors where a VM already has reservations.
func getVMExistingReservationHypervisors(vmUUID string, allFailoverReservations []v1alpha1.Reservation) map[string]bool {
	hypervisors := make(map[string]bool)
	for _, res := range allFailoverReservations {
		allocations := getFailoverAllocations(&res)
		if _, exists := allocations[vmUUID]; exists {
			hypervisors[res.Status.Host] = true
		}
	}
	return hypervisors
}

// vmIsOnHypervisor checks if a VM is on a specific hypervisor.
func vmIsOnHypervisor(vmUUID, hypervisor string, vmHypervisorsMap map[string]map[string]bool) bool {
	hypervisors := vmHypervisorsMap[vmUUID]
	if hypervisors == nil {
		return false
	}
	return hypervisors[hypervisor]
}

// vmIsOnAnyHypervisor checks if a VM is on any of the given hypervisors.
func vmIsOnAnyHypervisor(vmUUID string, hypervisors map[string]bool, vmHypervisorsMap map[string]map[string]bool) bool {
	vmHypervisors := vmHypervisorsMap[vmUUID]
	if vmHypervisors == nil {
		return false
	}
	for hypervisor := range vmHypervisors {
		if hypervisors[hypervisor] {
			return true
		}
	}
	return false
}

// getFailoverAllocations safely returns the allocations map from a failover reservation.
// Returns an empty map if the reservation has no failover status or allocations.
func getFailoverAllocations(res *v1alpha1.Reservation) map[string]string {
	if res.Status.FailoverReservation == nil || res.Status.FailoverReservation.Allocations == nil {
		return map[string]string{}
	}
	return res.Status.FailoverReservation.Allocations
}

// ============================================================================
// Eligibility Checks (Constraint-based)
// ============================================================================

// IsVMEligibleForReservation checks if a VM is eligible to use a specific reservation.
// A VM is eligible if it satisfies all the following constraints:
// (1) A VM cannot reserve a slot on its own hypervisor.
// (2) A VM's N reservation slots must be placed on N distinct hypervisors (no hypervisor overlap among slots).
// (3) For any reservation r, no two VMs that use r may be on the same hypervisor (directly or potentially via a reservation).
//
//	This means we check both the current hypervisor AND all reservation hypervisors of each VM.
//
// (4) For VM v with slots R = {r1..rn}, any other VM vi that uses any rj must not run on hs(v) nor on any hs(rj).
// (5) For VM v with slots R = {r1..rn}, there exist no vm_1, vm_2 (vm_1 != v and vm_2 != v)
//
//	with vm_1 uses r_j and vm_2 uses r_k and hypervisor(vm_1) = hypervisor(vm_2).
//	In other words: Among all VMs (other than v) that use ANY of v's slots, no two may run on the same hypervisor.
func IsVMEligibleForReservation(vm VM, reservation v1alpha1.Reservation, allFailoverReservations []v1alpha1.Reservation) bool {
	// Build VM hypervisors map from reservations, the candidate reservation, and the VM we are checking
	vmHypervisorsMap := buildVMHypervisorsMap(vm, reservation, allFailoverReservations)

	// Get allocations for this reservation
	resAllocations := getFailoverAllocations(&reservation)

	// Skip if this VM is already using this reservation
	if _, exists := resAllocations[vm.UUID]; exists {
		return false
	}

	// Constraint (1): A VM cannot reserve a slot on its own hypervisor
	// Use Status.Host for the actual hypervisor where the reservation is placed
	if reservation.Status.Host == vm.CurrentHypervisor {
		return false
	}

	// Get the hypervisors where this VM already has reservations
	vmExistingReservationHypervisors := getVMExistingReservationHypervisors(vm.UUID, allFailoverReservations)

	// Constraint (2): A VM's N reservation slots must be on N distinct hypervisors
	// Check if VM already has a reservation on this hypervisor
	if vmExistingReservationHypervisors[reservation.Status.Host] {
		return false
	}

	// Constraint (3): For any reservation r, no two VMs that use r may be on the same hypervisor
	// Only check VMs that are using this reservation
	for usedByVM := range resAllocations {
		if usedByVM == vm.UUID {
			continue
		}
		// Check if we already share any reservation with this VM
		for _, otherRes := range allFailoverReservations {
			otherResAllocations := getFailoverAllocations(&otherRes)
			_, vmUsesThis := otherResAllocations[vm.UUID]
			_, otherVMUsesThis := otherResAllocations[usedByVM]
			if vmUsesThis && otherVMUsesThis {
				// We already share a reservation with this VM
				// Check if the candidate reservation is on their hypervisor
				if vmIsOnHypervisor(usedByVM, reservation.Status.Host, vmHypervisorsMap) {
					return false
				}
			}
		}
	}

	// Constraint (4): Any other VM vi that uses any rj must not run on hs(v) nor on any hs(rj)
	// For VM v with slots R = {r1..rn}, any other VM vi that uses any rj must not run on:
	// - hs(v) = v's current hypervisor
	// - any hs(rj) = any of v's slot hypervisors (including the candidate reservation's hypervisor)

	// Build the set of all v's slot hypervisors (existing + candidate)
	vSlotHypervisors := make(map[string]bool)
	for hypervisor := range vmExistingReservationHypervisors {
		vSlotHypervisors[hypervisor] = true
	}
	vSlotHypervisors[reservation.Status.Host] = true // Add candidate reservation's hypervisor

	// Constraint (4) and (5) combined:
	// Collect all VMs (other than v) that use each of v's slots
	// We need to track which VMs use which slots to detect if a VM uses multiple slots
	vmsUsingVSlotsCount := make(map[string]int) // VM UUID -> count of v's slots it uses

	// VMs using v's existing reservations
	for _, otherRes := range allFailoverReservations {
		otherResAllocations := getFailoverAllocations(&otherRes)
		if _, vmUsesThis := otherResAllocations[vm.UUID]; vmUsesThis {
			for otherVM := range otherResAllocations {
				if otherVM != vm.UUID {
					vmsUsingVSlotsCount[otherVM]++
				}
			}
		}
	}

	// VMs using the candidate reservation
	for usedByVM := range resAllocations {
		if usedByVM != vm.UUID {
			vmsUsingVSlotsCount[usedByVM]++
		}
	}

	// Check Constraint (4): no VM using v's slots runs on hs(v) or any hs(rj)
	for otherVM := range vmsUsingVSlotsCount {
		// Check if they run on v's hypervisor
		if vmIsOnHypervisor(otherVM, vm.CurrentHypervisor, vmHypervisorsMap) {
			return false
		}
		// Check if they run on any of v's slot hypervisors
		if vmIsOnAnyHypervisor(otherVM, vSlotHypervisors, vmHypervisorsMap) {
			return false
		}
	}

	// Constraint (5): For VM v with slots R = {r1..rn}, there exist no vm_j, vm_k (vm_j != v and vm_k != v)
	// with vm_j uses r_j and vm_k uses r_k and hypervisor(vm_j) = hypervisor(vm_k).
	// Note: vm_j and vm_k CAN be the same VM! This means no VM (other than v) can use more than one of v's slots,
	// AND no two different VMs using v's slots can run on the same hypervisor.

	// Check constraint: no vm_j, vm_k with hypervisor(vm_j) = hypervisor(vm_k)
	// This includes the case where vm_j = vm_k (same VM using multiple slots)
	hypervisorToVM := make(map[string]string)
	for otherVM, slotCount := range vmsUsingVSlotsCount {
		// If a VM uses more than one of v's slots, it violates the constraint
		// (vm_j = vm_k = otherVM, and hypervisor(vm_j) = hypervisor(vm_k) trivially)
		if slotCount > 1 {
			return false
		}

		// Check if any two different VMs using v's slots run on the same hypervisor
		// A VM can be on multiple hypervisors, so we need to check all of them
		otherVMHypervisors := vmHypervisorsMap[otherVM]
		for otherVMHypervisor := range otherVMHypervisors {
			if existingVM, exists := hypervisorToVM[otherVMHypervisor]; exists {
				// Two VMs using v's slots run on the same hypervisor
				_ = existingVM // Both otherVM and existingVM are on otherVMHypervisor
				return false
			}
			hypervisorToVM[otherVMHypervisor] = otherVM
		}
	}

	// All constraints passed
	return true
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

	var eligible []v1alpha1.Reservation
	for _, res := range failoverReservations {
		if DoesVMFitInReservation(vm, res) && IsVMEligibleForReservation(vm, res, failoverReservations) {
			eligible = append(eligible, res)
		}
	}

	return eligible
}

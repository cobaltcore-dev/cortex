// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// DependencyGraph encapsulates the data structures needed for eligibility checking.
// It tracks relationships between VMs, reservations, and hypervisors.
type DependencyGraph struct {
	// vmToReservations maps vm_uuid -> set of reservation keys (namespace/name) the VM uses
	vmToReservations map[string]map[string]bool
	// vmToCurrentHypervisor maps vm_uuid -> current hypervisor (where VM is now)
	vmToCurrentHypervisor map[string]string
	// vmToReservationHosts maps vm_uuid -> set of reservation hosts (where VM could evacuate)
	vmToReservationHosts map[string]map[string]bool
	// reservationToVMs maps reservation key (namespace/name) -> set of vm_uuids using it
	reservationToVMs map[string]map[string]bool
	// reservationToHost maps reservation key (namespace/name) -> hypervisor where reservation is placed
	reservationToHost map[string]string
}

// reservationKey returns a unique key for a reservation (namespace/name).
// This prevents collisions between same-named reservations in different namespaces.
func reservationKey(namespace, name string) string {
	return namespace + "/" + name
}

// newDependencyGraph builds a DependencyGraph for eligibility checking.
// The VM is treated as already being part of the candidate reservation.
func newDependencyGraph(
	vm VM,
	candidateReservation v1alpha1.Reservation,
	allFailoverReservations []v1alpha1.Reservation,
) *DependencyGraph {

	g := &DependencyGraph{
		vmToReservations:      make(map[string]map[string]bool),
		vmToCurrentHypervisor: make(map[string]string),
		vmToReservationHosts:  make(map[string]map[string]bool),
		reservationToVMs:      make(map[string]map[string]bool),
		reservationToHost:     make(map[string]string),
	}

	// Process all reservations
	for _, res := range allFailoverReservations {
		resKey := reservationKey(res.Namespace, res.Name)
		resHost := res.Status.Host

		g.ensureResInMaps(resKey)
		g.reservationToHost[resKey] = resHost

		allocations := getFailoverAllocations(&res)
		for vmUUID, vmHypervisor := range allocations {
			g.ensureVMInMaps(vmUUID)
			g.vmToReservations[vmUUID][resKey] = true
			g.vmToCurrentHypervisor[vmUUID] = vmHypervisor
			g.vmToReservationHosts[vmUUID][resHost] = true
			g.reservationToVMs[resKey][vmUUID] = true
		}
	}

	// Process candidate reservation (may not be in allFailoverReservations)
	candidateResKey := reservationKey(candidateReservation.Namespace, candidateReservation.Name)
	candidateResHost := candidateReservation.Status.Host

	g.ensureResInMaps(candidateResKey)
	g.reservationToHost[candidateResKey] = candidateResHost

	candidateAllocations := getFailoverAllocations(&candidateReservation)
	for vmUUID, vmHypervisor := range candidateAllocations {
		g.ensureVMInMaps(vmUUID)
		g.vmToReservations[vmUUID][candidateResKey] = true
		g.vmToCurrentHypervisor[vmUUID] = vmHypervisor
		g.vmToReservationHosts[vmUUID][candidateResHost] = true
		g.reservationToVMs[candidateResKey][vmUUID] = true
	}

	// Add the VM we're checking with its current hypervisor
	g.ensureVMInMaps(vm.UUID)
	g.vmToCurrentHypervisor[vm.UUID] = vm.CurrentHypervisor

	// KEY: Treat VM as already in the candidate reservation
	g.vmToReservations[vm.UUID][candidateResKey] = true
	g.vmToReservationHosts[vm.UUID][candidateResHost] = true
	g.reservationToVMs[candidateResKey][vm.UUID] = true

	return g
}

func (g *DependencyGraph) ensureVMInMaps(vmUUID string) {
	if g.vmToReservations[vmUUID] == nil {
		g.vmToReservations[vmUUID] = make(map[string]bool)
	}
	if g.vmToReservationHosts[vmUUID] == nil {
		g.vmToReservationHosts[vmUUID] = make(map[string]bool)
	}
}

func (g *DependencyGraph) ensureResInMaps(resName string) {
	if g.reservationToVMs[resName] == nil {
		g.reservationToVMs[resName] = make(map[string]bool)
	}
}

// checkAllVMConstraints checks if a single VM satisfies all constraints (1-5).
// Returns true if the VM satisfies all constraints.
func (g *DependencyGraph) checkAllVMConstraints(vmUUID string) bool {
	vmCurrentHypervisor := g.vmToCurrentHypervisor[vmUUID]
	vmSlotHypervisors := g.vmToReservationHosts[vmUUID]

	// Constraint (1): A VM cannot reserve a slot on its own hypervisor
	if vmSlotHypervisors[vmCurrentHypervisor] {
		return false
	}

	// Constraint (2): A VM's N reservation slots must be on N distinct hypervisors
	numReservations := len(g.vmToReservations[vmUUID])
	numUniqueHosts := len(g.vmToReservationHosts[vmUUID])
	if numUniqueHosts < numReservations {
		return false
	}

	// Collect all VMs (other than this VM) that use any of this VM's slots
	vmsUsingVMSlotsCount := make(map[string]int)
	for resName := range g.vmToReservations[vmUUID] {
		for otherVM := range g.reservationToVMs[resName] {
			if otherVM != vmUUID {
				vmsUsingVMSlotsCount[otherVM]++
			}
		}
	}

	// Constraint (4): Any other VM that uses this VM's slots must not run on:
	// - this VM's current hypervisor
	// - any of this VM's slot hypervisors
	for otherVM := range vmsUsingVMSlotsCount {
		otherVMCurrentHypervisor := g.vmToCurrentHypervisor[otherVM]

		if otherVMCurrentHypervisor == vmCurrentHypervisor {
			return false
		}

		if vmSlotHypervisors[otherVMCurrentHypervisor] {
			return false
		}
	}

	// Constraint (5): No two VMs (other than this VM) using this VM's slots can be on the same hypervisor
	hypervisorToVM := make(map[string]string)
	for otherVM, slotCount := range vmsUsingVMSlotsCount {
		if slotCount > 1 {
			return false
		}

		otherVMCurrentHypervisor := g.vmToCurrentHypervisor[otherVM]
		if existingVM, exists := hypervisorToVM[otherVMCurrentHypervisor]; exists && existingVM != otherVM {
			return false
		}
		hypervisorToVM[otherVMCurrentHypervisor] = otherVM
	}

	return true
}

// isVMEligibleForReservation checks if all VMs in the candidate reservation satisfy constraints.
func (g *DependencyGraph) isVMEligibleForReservation(candidateResName string) bool {
	for vmUUID := range g.reservationToVMs[candidateResName] {
		if !g.checkAllVMConstraints(vmUUID) {
			return false
		}
	}
	return true
}

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

	// Build dependency graph (with VM already in the candidate reservation)
	graph := newDependencyGraph(vm, reservation, allFailoverReservations)

	// Check all constraints for all VMs in the reservation
	return graph.isVMEligibleForReservation(reservationKey(reservation.Namespace, reservation.Name))
}

// doesVMFitInReservation checks if a VM's resources fit within a reservation's resources.
// Returns true if all VM resources are less than or equal to the reservation's resources.
func doesVMFitInReservation(vm VM, reservation v1alpha1.Reservation) bool {
	// Check memory: VM's memory must be <= reservation's memory
	if vmMemory, ok := vm.Resources["memory"]; ok {
		if resMemory, ok := reservation.Spec.Resources[hv1.ResourceMemory]; ok {
			if vmMemory.Cmp(resMemory) > 0 {
				return false // VM memory exceeds reservation memory
			}
		} else {
			return false // Reservation has no memory resource defined
		}
	}

	// Check CPU: VM's vcpus must be <= reservation's cpu
	// Note: VM uses "vcpus" key, but reservations use "cpu" as the canonical key.
	if vmVCPUs, ok := vm.Resources["vcpus"]; ok {
		if resCPU, ok := reservation.Spec.Resources[hv1.ResourceCPU]; ok {
			if vmVCPUs.Cmp(resCPU) > 0 {
				return false // VM vcpus exceeds reservation cpu
			}
		} else {
			return false // Reservation has no cpu resource defined
		}
	}

	return true
}

// FindEligibleReservations finds all reservations that a VM is eligible to use.
// It checks both resource fit and eligibility constraints.
func FindEligibleReservations(
	vm VM,
	failoverReservations []v1alpha1.Reservation,
) []v1alpha1.Reservation {
	//TODO: we create data mappings inside IsVMEligibleForReservation those should probably be done already on this level to avoid redundant work
	var eligible []v1alpha1.Reservation
	for _, res := range failoverReservations {
		if doesVMFitInReservation(vm, res) && IsVMEligibleForReservation(vm, res, failoverReservations) {
			eligible = append(eligible, res)
		}
	}

	return eligible
}

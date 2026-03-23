// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"slices"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// TODO: The dependency graph does not capture that we can have a senario where the VM=>Host mapping is different across multipel reservations.
// We currently set  vmToCurrentHypervisor based on the last vm => host mapping we see in a reservation
// the periodic reconciler should remove all vm : host mapping that do not match the current host of a vm before we end up here.

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

// IsVMEligibleForReservation checks if a VM is eligible to use a specific reservation.
// A VM is eligible if it satisfies all the following constraints:
// (1) A VM cannot reserve a slot on its own hypervisor.
// (2) A VM's N reservation slots must be placed on N distinct hypervisors.
// (3) For any reservation r, no two VMs that use r may be on the same hypervisor.
// (4) For VM v with slots R, any other VM that uses any slot must not run on v's host or slot hosts.
// (5) For VM v with slots R, no two other VMs using v's slots can be on the same hypervisor.
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

	graph := newDependencyGraph(vm, reservation, allFailoverReservations)
	return graph.isVMEligibleForReservation(reservationKey(reservation.Namespace, reservation.Name))
}

// CheckVMsStillEligible checks if VMs in reservations are still eligible.
// Returns a map of reservation name -> list of VM UUIDs that are no longer eligible.
func CheckVMsStillEligible(
	vms map[string]VM,
	failoverReservations []v1alpha1.Reservation,
) map[string][]string {

	baseGraph := newBaseDependencyGraph(failoverReservations)
	result := make(map[string][]string)

	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		resKey := reservationKey(res.Namespace, res.Name)

		// Sort VM UUIDs for deterministic iteration order
		vmUUIDs := make([]string, 0, len(allocations))
		for vmUUID := range allocations {
			vmUUIDs = append(vmUUIDs, vmUUID)
		}
		slices.Sort(vmUUIDs)

		for _, vmUUID := range vmUUIDs {
			vm, vmExists := vms[vmUUID]
			if !vmExists {
				continue
			}

			isEligible := false
			if vm.CurrentHypervisor == baseGraph.vmToCurrentHypervisor[vmUUID] {
				isEligible = baseGraph.isVMEligibleForReservation(resKey)
			}

			if !isEligible {
				result[res.Name] = append(result[res.Name], vmUUID)
				baseGraph.removeVMFromReservation(vm.UUID, resKey, res.Status.Host)
			}
		}
	}

	return result
}

// FindEligibleReservations finds all reservations that a VM is eligible to use.
func FindEligibleReservations(
	vm VM,
	failoverReservations []v1alpha1.Reservation,
) []v1alpha1.Reservation {

	baseGraph := newBaseDependencyGraph(failoverReservations)

	var eligible []v1alpha1.Reservation
	for _, res := range failoverReservations {
		if !doesVMFitInReservation(vm, res) {
			continue
		}

		resAllocations := getFailoverAllocations(&res)
		if _, exists := resAllocations[vm.UUID]; exists {
			continue
		}

		candidateResKey := reservationKey(res.Namespace, res.Name)
		candidateResHost := res.Status.Host

		baseGraph.addVMToReservation(vm.UUID, vm.CurrentHypervisor, candidateResKey, candidateResHost)
		isEligible := baseGraph.isVMEligibleForReservation(candidateResKey)
		baseGraph.removeVMFromReservation(vm.UUID, candidateResKey, candidateResHost)

		if isEligible {
			eligible = append(eligible, res)
		}
	}

	return eligible
}

// reservationKey returns a unique key for a reservation (namespace/name).
func reservationKey(namespace, name string) string {
	return namespace + "/" + name
}

// newBaseDependencyGraph builds a base DependencyGraph from all reservations.
func newBaseDependencyGraph(allFailoverReservations []v1alpha1.Reservation) *DependencyGraph {
	g := &DependencyGraph{
		vmToReservations:      make(map[string]map[string]bool),
		vmToCurrentHypervisor: make(map[string]string),
		vmToReservationHosts:  make(map[string]map[string]bool),
		reservationToVMs:      make(map[string]map[string]bool),
		reservationToHost:     make(map[string]string),
	}

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

	return g
}

// newDependencyGraph builds a DependencyGraph with the VM added to the candidate reservation.
func newDependencyGraph(
	vm VM,
	candidateReservation v1alpha1.Reservation,
	allFailoverReservations []v1alpha1.Reservation,
) *DependencyGraph {

	g := newBaseDependencyGraph(allFailoverReservations)

	candidateResKey := reservationKey(candidateReservation.Namespace, candidateReservation.Name)
	candidateResHost := candidateReservation.Status.Host

	g.addVMToReservation(vm.UUID, vm.CurrentHypervisor, candidateResKey, candidateResHost)

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

// addVMToReservation adds a VM to a reservation in the graph.
func (g *DependencyGraph) addVMToReservation(vmUUID, vmHypervisor, resKey, resHost string) {
	g.ensureVMInMaps(vmUUID)
	g.ensureResInMaps(resKey)
	g.vmToCurrentHypervisor[vmUUID] = vmHypervisor
	g.vmToReservations[vmUUID][resKey] = true
	g.vmToReservationHosts[vmUUID][resHost] = true
	g.reservationToVMs[resKey][vmUUID] = true
}

// removeVMFromReservation removes a VM from a reservation in the graph.
func (g *DependencyGraph) removeVMFromReservation(vmUUID, resKey, resHost string) {
	delete(g.vmToReservations[vmUUID], resKey)
	delete(g.vmToReservationHosts[vmUUID], resHost)
	delete(g.reservationToVMs[resKey], vmUUID)
}

// checkAllVMConstraints checks if a single VM satisfies all constraints (1-5).
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

// doesVMFitInReservation checks if a VM's resources fit within a reservation's resources.
func doesVMFitInReservation(vm VM, reservation v1alpha1.Reservation) bool {
	if vmMemory, ok := vm.Resources["memory"]; ok {
		if resMemory, ok := reservation.Spec.Resources[hv1.ResourceMemory]; ok {
			if vmMemory.Cmp(resMemory) > 0 {
				return false
			}
		} else {
			return false
		}
	}

	if vmVCPUs, ok := vm.Resources["vcpus"]; ok {
		if resCPU, ok := reservation.Spec.Resources[hv1.ResourceCPU]; ok {
			if vmVCPUs.Cmp(resCPU) > 0 {
				return false
			}
		} else {
			return false
		}
	}

	return true
}

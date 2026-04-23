// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// resolvedReservationSpec holds the resolved resource spec for scheduling and reservation sizing.
// When UseFlavorGroupResources is enabled and the VM's flavor is found in a group,
// resources are sized to the LargestFlavor. Otherwise, they come from the VM directly.
type resolvedReservationSpec struct {
	FlavorName      string // may be overridden to LargestFlavor.Name
	FlavorGroupName string // flavor group name if found, empty otherwise
	MemoryMB        uint64
	VCPUs           uint64
}

// ResourceGroup returns the flavor group name if available, otherwise falls back to the provided fallback value.
func (r resolvedReservationSpec) ResourceGroup(fallback string) string {
	if r.FlavorGroupName != "" {
		return r.FlavorGroupName
	}
	return fallback
}

// HypervisorResources returns the reservation spec resources as a map suitable for the reservation CRD.
// We use "cpu" (not "vcpus") as the canonical key because the scheduling capacity logic
// (e.g., nova filter_has_enough_capacity) uses "cpu".
func (r resolvedReservationSpec) HypervisorResources() map[hv1.ResourceName]resource.Quantity {
	return map[hv1.ResourceName]resource.Quantity{
		"memory": *resource.NewQuantity(int64(r.MemoryMB)*1024*1024, resource.BinarySI), //nolint:gosec // flavor memory from specs, realistically bounded
		"cpu":    *resource.NewQuantity(int64(r.VCPUs), resource.DecimalSI),             //nolint:gosec // flavor vcpus from specs, realistically bounded
	}
}

// resolveVMSpecForScheduling resolves the VM's resources for scheduling.
// When useFlavorGroupResources is true and the flavor is found in a group,
// returns the LargestFlavor's name and size. Otherwise falls back to VM resources.
func resolveVMSpecForScheduling(
	ctx context.Context,
	vm VM,
	useFlavorGroupResources bool,
	flavorGroups map[string]compute.FlavorGroupFeature,
) resolvedReservationSpec {

	logger := LoggerFromContext(ctx)

	if useFlavorGroupResources && flavorGroups != nil {
		groupName, _, err := reservations.FindFlavorInGroups(vm.FlavorName, flavorGroups)
		if err == nil {
			fg := flavorGroups[groupName]
			largest := fg.LargestFlavor
			logger.V(1).Info("resolved VM resources from flavor group LargestFlavor",
				"vmFlavor", vm.FlavorName,
				"flavorGroup", groupName,
				"largestFlavor", largest.Name,
				"memoryMB", largest.MemoryMB,
				"vcpus", largest.VCPUs)
			return resolvedReservationSpec{
				FlavorName:      largest.Name,
				FlavorGroupName: groupName,
				MemoryMB:        largest.MemoryMB,
				VCPUs:           largest.VCPUs,
			}
		}
		logger.Info("flavor group lookup failed, falling back to VM resources",
			"vmFlavor", vm.FlavorName,
			"error", err)
	}

	// Fallback: use VM's own resources
	var memoryMB, vcpus uint64
	if memory, ok := vm.Resources["memory"]; ok {
		memoryMB = uint64(memory.Value() / (1024 * 1024)) //nolint:gosec // memory values won't overflow
	}
	if v, ok := vm.Resources["vcpus"]; ok {
		vcpus = uint64(v.Value()) //nolint:gosec // vcpus values won't overflow
	}
	return resolvedReservationSpec{
		FlavorName: vm.FlavorName,
		MemoryMB:   memoryMB,
		VCPUs:      vcpus,
	}
}

// getFailoverAllocations safely returns the allocations map from a failover reservation.
// Returns an empty map if the reservation has no failover status or allocations.
func getFailoverAllocations(res *v1alpha1.Reservation) map[string]string {
	if res.Status.FailoverReservation == nil || res.Status.FailoverReservation.Allocations == nil {
		return map[string]string{}
	}
	return res.Status.FailoverReservation.Allocations
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

// addVMToReservation creates a copy of a reservation with the VM added to its allocations.
// The original reservation is NOT modified.
func addVMToReservation(reservation v1alpha1.Reservation, vm VM) *v1alpha1.Reservation {
	// Deep copy the reservation
	updatedRes := reservation.DeepCopy()

	// Initialize the FailoverReservation status if needed
	if updatedRes.Status.FailoverReservation == nil {
		updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
	}
	// Initialize the Allocations map if needed
	if updatedRes.Status.FailoverReservation.Allocations == nil {
		updatedRes.Status.FailoverReservation.Allocations = make(map[string]string)
	}
	// Add the VM to the allocations
	updatedRes.Status.FailoverReservation.Allocations[vm.UUID] = vm.CurrentHypervisor

	// Mark the reservation as changed and not yet acknowledged
	now := metav1.Now()
	updatedRes.Status.FailoverReservation.LastChanged = &now
	updatedRes.Status.FailoverReservation.AcknowledgedAt = nil

	return updatedRes
}

// ValidateFailoverReservationResources validates that a failover reservation has valid resource keys.
// Returns an error if the reservation has invalid resource keys (only "cpu" and "memory" are allowed).
// This ensures reservations are properly considered by the scheduling filters.
func ValidateFailoverReservationResources(res *v1alpha1.Reservation) error {
	if res.Spec.Resources == nil {
		return nil // No resources is valid (will be caught elsewhere if needed)
	}

	allowedKeys := map[hv1.ResourceName]bool{"cpu": true, "memory": true}
	for key := range res.Spec.Resources {
		if !allowedKeys[key] {
			return fmt.Errorf("invalid resource key %q: only 'cpu' and 'memory' are allowed", key)
		}
	}
	return nil
}

// newFailoverReservation creates a new failover reservation for a VM on a specific hypervisor.
// This does NOT persist the reservation to the cluster - it only creates the in-memory object.
// The caller is responsible for persisting the reservation.
//
// The resolved parameter contains the pre-computed resources (from resolveVMForScheduling),
// which may come from the VM's flavor group LargestFlavor or from the VM's own resources.
// This ensures the same sizing is used for both the scheduler query and the reservation CRD.
func newFailoverReservation(
	ctx context.Context,
	vm VM,
	hypervisor, creator string,
	resSpec resolvedReservationSpec,
) *v1alpha1.Reservation {

	logger := LoggerFromContext(ctx)

	resources := resSpec.HypervisorResources()

	reservation := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "failover-",
			Labels: map[string]string{
				"cortex.cloud/creator":        creator,
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailover,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeFailover,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			AvailabilityZone: vm.AvailabilityZone,
			Resources:        resources,
			TargetHost:       hypervisor, // Set the desired hypervisor from scheduler response
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: resSpec.ResourceGroup(vm.FlavorName),
			},
		},
	}

	// Set the status with the initial allocation (in-memory only)
	now := metav1.Now()
	reservation.Status.Host = hypervisor
	reservation.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
		Allocations: map[string]string{
			vm.UUID: vm.CurrentHypervisor,
		},
		LastChanged:    &now,
		AcknowledgedAt: nil,
	}
	// Set the Ready condition
	reservation.Status.Conditions = []metav1.Condition{
		{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ReservationActive",
			Message:            "Failover reservation is active and ready",
			LastTransitionTime: metav1.Now(),
		},
	}

	logger.V(1).Info("built new failover reservation",
		"vmUUID", vm.UUID,
		"hypervisor", hypervisor,
		"resources", resources)

	return reservation
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
func newFailoverReservation(ctx context.Context, vm VM, hypervisor, creator string) *v1alpha1.Reservation {
	logger := LoggerFromContext(ctx)

	// Build resources from VM's Resources map
	// The VM struct uses "vcpus" and "memory" keys (see vm_source.go)
	// We convert "vcpus" to "cpu" for the reservation because the scheduling capacity logic
	// (e.g., nova filter_has_enough_capacity) uses "cpu" as the canonical key.

	// TODO we may want to use different resource (bigger) to enable better sharing
	resources := make(map[hv1.ResourceName]resource.Quantity)
	if memory, ok := vm.Resources["memory"]; ok {
		resources["memory"] = memory
	}
	if vcpus, ok := vm.Resources["vcpus"]; ok {
		// todo check if that is correct, i.e. that the cpu reported on e.g. hypervisors is vcpu and not pcpu
		resources["cpu"] = vcpus
	}

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
			AvailabilityZone: vm.AvailabilityZone,
			Resources:        resources,
			TargetHost:       hypervisor, // Set the desired hypervisor from scheduler response
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: vm.FlavorName,
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

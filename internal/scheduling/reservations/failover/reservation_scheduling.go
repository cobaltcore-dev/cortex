// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"
	"slices"
	"sort"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FIXME (probably after GA):
/*
We have roughly the following flow:
* For each VM vm:
	- request to cortex placement API (with roughly all host), where could i spawn vm
	  - (i1) issue: we exclude host here that are "full" but have a reservation we could share
	- (1) for each reservation on any of the returned hosts
		- check if the reservation is eligible for the vm and if so reuse
		- issue: same as (i2)
	- [if nothing in (1)] for each host h in the response:
		- check if we can create a new reservation on h
			- yes: break and create reservation
			- (i2) issue: between creation of reservation and actual scheduling we may have place a vm on h and no vm can not be placed on h anymore (e.g. capacity anti affinity, etc)
solution is probably some kind of pending state, where the reservation is already enforced in the pipeline and we are waiting for ack (and if nack the reservation gets deleted)

*/

// Pipeline names for failover reservation scheduling
const (
	// PipelineReuseFailoverReservation is used to check if a VM can reuse an existing reservation.
	// It validates host compatibility without checking capacity (since reservation already has capacity).
	PipelineReuseFailoverReservation = "kvm-valid-host-reuse-failover-reservation"

	// PipelineNewFailoverReservation is used to find a host for creating a new reservation.
	// It validates host compatibility AND checks capacity.
	PipelineNewFailoverReservation = "kvm-valid-host-new-failover-reservation"
)

func (c *FailoverReservationController) getPotentialHypervisorsForVM(ctx context.Context, vm VM, allHypervisors []string, failoverReservations []v1alpha1.Reservation, pipeline string) ([]string, error) {
	// todo * add unit with ScheduleReservation mocked
	// Build set of hypervisors where this VM already has reservations
	vmReservationHypervisors := make(map[string]bool)
	for _, res := range failoverReservations {
		allocations := getFailoverAllocations(&res)
		if _, exists := allocations[vm.UUID]; exists {
			vmReservationHypervisors[res.Status.Host] = true
		}
	}

	// Build list of eligible hypervisors:
	// - Not the VM's current hypervisor
	// - Not a hypervisor where the VM already has a reservation
	eligibleHypervisors := make([]api.ExternalSchedulerHost, 0, len(allHypervisors))
	ignoreHypervisors := []string{vm.CurrentHypervisor}
	for _, hypervisor := range allHypervisors {
		if hypervisor == vm.CurrentHypervisor {
			continue // VM's current hypervisor
		}
		if vmReservationHypervisors[hypervisor] {
			ignoreHypervisors = append(ignoreHypervisors, hypervisor)
			continue // VM already has a reservation on this hypervisor
		}
		eligibleHypervisors = append(eligibleHypervisors, api.ExternalSchedulerHost{
			ComputeHost: hypervisor,
		})
	}

	if len(eligibleHypervisors) == 0 {
		return nil, fmt.Errorf("no eligible hypervisors for failover reservation (VM is on %s, already has reservations on %d hypervisors)", vm.CurrentHypervisor, len(vmReservationHypervisors))
	}

	// Get memory and vcpus from VM resources
	// The VM struct uses "vcpus" and "memory" keys (see vm_source.go)
	var memoryMB uint64
	var vcpus uint64
	if memory, ok := vm.Resources["memory"]; ok {
		// Convert from bytes to MB
		memoryMB = uint64(memory.Value() / (1024 * 1024)) //nolint:gosec // memory values won't overflow
	}
	if vcpusRes, ok := vm.Resources["vcpus"]; ok {
		vcpus = uint64(vcpusRes.Value()) //nolint:gosec // vcpus values won't overflow
	}

	// Build flavor extra specs from VM's extra specs
	// Start with the VM's actual extra specs, then ensure required defaults are set
	flavorExtraSpecs := make(map[string]string)
	for k, v := range vm.FlavorExtraSpecs {
		flavorExtraSpecs[k] = v
	}
	// Ensure hypervisor_type is set for KVM scheduling if not already present
	if _, ok := flavorExtraSpecs["capabilities:hypervisor_type"]; !ok {
		flavorExtraSpecs["capabilities:hypervisor_type"] = "qemu"
	}

	// Schedule the reservation using the SchedulerClient
	scheduleReq := reservations.ScheduleReservationRequest{
		InstanceUUID:     "failover-" + vm.UUID,
		ProjectID:        vm.ProjectID,
		FlavorName:       vm.FlavorName,
		FlavorExtraSpecs: flavorExtraSpecs,
		MemoryMB:         memoryMB,
		VCPUs:            vcpus,
		EligibleHosts:    eligibleHypervisors,
		IgnoreHosts:      ignoreHypervisors,
		Pipeline:         pipeline,
	}

	log.Info("scheduling failover reservation",
		"vmUUID", vm.UUID,
		"pipeline", pipeline,
		"eligibleHypervisors", len(eligibleHypervisors),
		"ignoreHypervisors", ignoreHypervisors)

	scheduleResp, err := c.SchedulerClient.ScheduleReservation(ctx, scheduleReq)
	if err != nil {
		return nil, fmt.Errorf("failed to schedule failover reservation: %w", err)
	}

	return scheduleResp.Hosts, nil
}

// tryReuseExistingReservation finds an existing reservation that can be reused for a VM.
// It returns a copy of the reservation with the VM added to its allocations (in-memory only, not persisted).
// The original reservation in the input slice is NOT modified.
// The caller is responsible for persisting the changes to the cluster.
func (c *FailoverReservationController) tryReuseExistingReservation(
	ctx context.Context,
	vm VM,
	failoverReservations []v1alpha1.Reservation,
	allHypervisors []string,
) *v1alpha1.Reservation {

	hypervisors, err := c.getPotentialHypervisorsForVM(ctx, vm, allHypervisors, failoverReservations, PipelineReuseFailoverReservation)
	if err != nil {
		log.Error(err, "failed to get potential hypervisors for VM", "vmUUID", vm.UUID)
		return nil
	}
	if len(hypervisors) == 0 {
		log.Info("no potential hypervisors returned by scheduler for VM", "vmUUID", vm.UUID)
		return nil
	}

	eligibleRes := FindEligibleReservations(vm, failoverReservations)
	if len(eligibleRes) == 0 {
		log.Info("no eligible reservations found for VM", "vmUUID", vm.UUID)
		return nil
	}

	// Sort reservations by number of allocations (prefer reservations with more VMs for better sharing)
	sort.Slice(eligibleRes, func(i, j int) bool {
		iAllocs := len(getFailoverAllocations(&eligibleRes[i]))
		jAllocs := len(getFailoverAllocations(&eligibleRes[j]))
		return iAllocs > jAllocs // Descending order - more allocations first
	})

	for _, reservation := range eligibleRes {
		log.Info("checking existing reservation for eligibility",
			"vmUUID", vm.UUID,
			"reservationName", reservation.Name,
			"reservationHypervisor", reservation.Status.Host)
		if slices.Contains(hypervisors, reservation.Status.Host) {
			// Create a copy of the reservation with the VM added
			updatedRes := buildReservationWithVM(reservation, vm)
			log.Info("found reusable reservation for VM",
				"vmUUID", vm.UUID,
				"reservationName", updatedRes.Name,
				"hypervisor", updatedRes.Status.Host)
			return updatedRes
		}
	}
	return nil
}

// buildReservationWithVM creates a copy of a reservation with the VM added to its allocations.
// The original reservation is NOT modified.
func buildReservationWithVM(reservation v1alpha1.Reservation, vm VM) *v1alpha1.Reservation {
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

	return updatedRes
}

// buildNewFailoverReservation builds a new failover reservation for a VM on a specific hypervisor.
// This does NOT persist the reservation to the cluster - it only creates the in-memory object.
// The caller is responsible for persisting the reservation.
func (c *FailoverReservationController) buildNewFailoverReservation(vm VM, hypervisor string) *v1alpha1.Reservation {
	// Build resources from VM's Resources map
	// The VM struct uses "vcpus" and "memory" keys (see vm_source.go)

	// TODO we may want to use different resource (bigger) to enable better sharing
	resources := make(map[string]resource.Quantity)
	if memory, ok := vm.Resources["memory"]; ok {
		resources["memory"] = memory
	}
	if vcpus, ok := vm.Resources["vcpus"]; ok {
		resources["vcpus"] = vcpus
	}

	reservation := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "failover-",
			Labels: map[string]string{
				"cortex.sap.com/creator": c.Config.Creator,
				"cortex.sap.com/type":    "failover",
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			Resources:  resources,
			TargetHost: hypervisor, // Set the desired hypervisor from scheduler response
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: vm.FlavorName,
			},
		},
	}

	// Set the status with the initial allocation (in-memory only)
	reservation.Status.Host = hypervisor
	reservation.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
		Allocations: map[string]string{
			vm.UUID: vm.CurrentHypervisor,
		},
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

	log.Info("built new failover reservation",
		"vmUUID", vm.UUID,
		"hypervisor", hypervisor,
		"resources", resources)

	return reservation
}

// scheduleAndBuildNewFailoverReservation schedules a failover reservation for a VM.
// It only considers hypervisors that don't have the VM or a reservation for that VM on it.
// Returns the built reservation (in-memory only, not persisted).
// The caller is responsible for persisting the reservation to the cluster.
func (c *FailoverReservationController) scheduleAndBuildNewFailoverReservation(
	ctx context.Context,
	vm VM,
	allHypervisors []string,
	failoverReservations []v1alpha1.Reservation,
) (*v1alpha1.Reservation, error) {
	// Get potential hypervisors from scheduler
	potentialHypervisors, err := c.getPotentialHypervisorsForVM(ctx, vm, allHypervisors, failoverReservations, PipelineNewFailoverReservation)
	if err != nil {
		return nil, fmt.Errorf("failed to get potential hypervisors for VM: %w", err)
	}

	// Iterate through scheduler-returned hypervisors to find one that passes eligibility constraints
	var selectedHypervisor string
	for _, candidateHypervisor := range potentialHypervisors {
		// Check if the VM can create a new reservation on this hypervisor
		hypotheticalRes := v1alpha1.Reservation{
			Status: v1alpha1.ReservationStatus{
				Host: candidateHypervisor,
				// Empty FailoverReservation status - new reservation has no allocations
			},
		}
		if IsVMEligibleForReservation(vm, hypotheticalRes, failoverReservations) {
			selectedHypervisor = candidateHypervisor
			log.Info("VM can create new reservation on hypervisor",
				"vmUUID", vm.UUID,
				"hypervisor", candidateHypervisor)
			break
		}
	}

	if selectedHypervisor == "" {
		return nil, fmt.Errorf("no eligible hypervisors after constraint checking (scheduler returned %d hypervisors, all rejected)", len(potentialHypervisors))
	}

	log.Info("scheduler selected hypervisor for failover reservation",
		"vmUUID", vm.UUID,
		"selectedHypervisor", selectedHypervisor,
		"allReturnedHypervisors", potentialHypervisors)

	// Build the failover reservation on the selected hypervisor (in-memory only)
	reservation := c.buildNewFailoverReservation(vm, selectedHypervisor)

	return reservation, nil
}

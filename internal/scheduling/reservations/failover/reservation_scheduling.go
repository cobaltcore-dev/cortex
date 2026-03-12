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
)

// Pipeline names for failover reservation scheduling
const (
	// PipelineReuseFailoverReservation is used to check if a VM can reuse an existing reservation.
	// It validates host compatibility without checking capacity (since reservation already has capacity).
	PipelineReuseFailoverReservation = "kvm-valid-host-reuse-failover-reservation"

	// PipelineNewFailoverReservation is used to find a host for creating a new reservation.
	// It validates host compatibility AND checks capacity.
	PipelineNewFailoverReservation = "kvm-valid-host-new-failover-reservation"

	// PipelineAcknowledgeFailoverReservation is used to validate that a failover reservation
	// is still valid for all its allocated VMs. It sends an evacuation-style scheduling request
	// for each VM with only the reservation's host as the eligible target.
	PipelineAcknowledgeFailoverReservation = "kvm-acknowledge-failover-reservation"
)

func (c *FailoverReservationController) queryHypervisorsFromScheduler(ctx context.Context, vm VM, allHypervisors []string, pipeline string) ([]string, error) {
	// Build list of eligible hypervisors (excluding VM's current hypervisor)
	eligibleHypervisors := make([]api.ExternalSchedulerHost, 0, len(allHypervisors))
	for _, hypervisor := range allHypervisors {
		if hypervisor == vm.CurrentHypervisor {
			continue // VM's current hypervisor
		}
		eligibleHypervisors = append(eligibleHypervisors, api.ExternalSchedulerHost{
			ComputeHost: hypervisor,
		})
	}

	if len(eligibleHypervisors) == 0 {
		return nil, fmt.Errorf("no eligible hypervisors for failover reservation (VM is on %s)", vm.CurrentHypervisor)
	}

	ignoreHypervisors := []string{vm.CurrentHypervisor}

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

	// Schedule the reservation using the SchedulerClient.
	// Note: We pass all hypervisors (from all AZs) in EligibleHosts. The scheduler pipeline's
	// filter_correct_az filter will exclude hosts that are not in the VM's availability zone.
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
		AvailabilityZone: vm.AvailabilityZone,
	}

	log.V(1).Info("scheduling failover reservation",
		"vmUUID", vm.UUID,
		"pipeline", pipeline,
		"eligibleHypervisors", len(eligibleHypervisors),
		"ignoreHypervisors", ignoreHypervisors)

	scheduleResp, err := c.SchedulerClient.ScheduleReservation(ctx, scheduleReq)
	if err != nil {
		return nil, fmt.Errorf("failed to schedule failover reservation: %w", err)
	}

	log.V(1).Info("scheduling failover reservation",
		"vmUUID", vm.UUID,
		"pipeline", pipeline,
		"eligibleHypervisors", len(eligibleHypervisors),
		"ignoreHypervisors", ignoreHypervisors,
		"returnedHypervisors", scheduleResp.Hosts)

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

	valid_hypervisors, err := c.queryHypervisorsFromScheduler(ctx, vm, allHypervisors, PipelineReuseFailoverReservation)
	if err != nil {
		log.Error(err, "failed to get potential hypervisors for VM", "vmUUID", vm.UUID)
		return nil
	}
	if len(valid_hypervisors) == 0 {
		log.Info("no potential hypervisors returned by scheduler for VM", "vmUUID", vm.UUID)
		return nil
	}

	eligibleReservations := FindEligibleReservations(vm, failoverReservations)
	if len(eligibleReservations) == 0 {
		log.Info("no eligible reservations found for VM", "vmUUID", vm.UUID)
		return nil
	}

	// Sort reservations by number of allocations (prefer reservations with more VMs for better sharing)
	sort.Slice(eligibleReservations, func(i, j int) bool {
		iAllocs := len(getFailoverAllocations(&eligibleReservations[i]))
		jAllocs := len(getFailoverAllocations(&eligibleReservations[j]))
		return iAllocs > jAllocs // Descending order - more allocations first
	})

	for _, reservation := range eligibleReservations {
		log.V(2).Info("checking existing reservation for eligibility",
			"vmUUID", vm.UUID,
			"reservationName", reservation.Name,
			"reservationHypervisor", reservation.Status.Host)
		if slices.Contains(valid_hypervisors, reservation.Status.Host) {
			// Create a copy of the reservation with the VM added
			updatedRes := addVMToReservation(reservation, vm)
			log.V(1).Info("found reusable reservation for VM",
				"vmUUID", vm.UUID,
				"reservationName", updatedRes.Name,
				"hypervisor", updatedRes.Status.Host)
			return updatedRes
		}
	}
	return nil
}

// validateVmViaSchedulerEvacuation sends an evacuation-style scheduling request to validate
// that a VM can use the reservation host.
// TODO this is a bit of a hack. Ideally we have a special kind of request for that which would also verify that we equally are using the reservation
func (c *FailoverReservationController) validateVmViaSchedulerEvacuation(
	ctx context.Context,
	vm VM,
	reservationHost string,
	vmCurrentHost string,
) (bool, error) {
	// Get memory and vcpus from VM resources
	var memoryMB uint64
	var vcpus uint64
	if memory, ok := vm.Resources["memory"]; ok {
		memoryMB = uint64(memory.Value() / (1024 * 1024)) //nolint:gosec // memory values won't overflow
	}
	if vcpusRes, ok := vm.Resources["vcpus"]; ok {
		vcpus = uint64(vcpusRes.Value()) //nolint:gosec // vcpus values won't overflow
	}

	// Build flavor extra specs from VM's extra specs
	flavorExtraSpecs := make(map[string]string)
	for k, v := range vm.FlavorExtraSpecs {
		flavorExtraSpecs[k] = v
	}
	if _, ok := flavorExtraSpecs["capabilities:hypervisor_type"]; !ok {
		flavorExtraSpecs["capabilities:hypervisor_type"] = "qemu"
	}

	// Build a single-host request to validate the VM can use the reservation host
	scheduleReq := reservations.ScheduleReservationRequest{
		InstanceUUID:     "validate-" + vm.UUID,
		ProjectID:        vm.ProjectID,
		FlavorName:       vm.FlavorName,
		FlavorExtraSpecs: flavorExtraSpecs,
		MemoryMB:         memoryMB,
		VCPUs:            vcpus,
		EligibleHosts:    []api.ExternalSchedulerHost{{ComputeHost: reservationHost}},
		IgnoreHosts:      []string{vmCurrentHost},
		Pipeline:         PipelineAcknowledgeFailoverReservation,
		AvailabilityZone: vm.AvailabilityZone,
	}

	log.V(1).Info("validating VM via scheduler evacuation",
		"vmUUID", vm.UUID,
		"reservationHost", reservationHost,
		"vmCurrentHost", vmCurrentHost,
		"pipeline", PipelineAcknowledgeFailoverReservation)

	resp, err := c.SchedulerClient.ScheduleReservation(ctx, scheduleReq)
	if err != nil {
		return false, fmt.Errorf("failed to validate VM for reservation host: %w", err)
	}

	// Handle empty response - no hosts returned
	if len(resp.Hosts) < 1 {
		return false, nil
	}

	// Log unexpected scheduler responses
	if len(resp.Hosts) > 1 || resp.Hosts[0] != reservationHost {
		log.Error(nil, "scheduler returned unexpected hosts for single-host validation request",
			"vmUUID", vm.UUID,
			"reservationHost", reservationHost,
			"returnedHosts", resp.Hosts)
	}

	// If the reservation host is returned, the VM can use it
	return resp.Hosts[0] == reservationHost, nil
}

// scheduleAndBuildNewFailoverReservation schedules a failover reservation for a VM.
// Returns the built reservation (in-memory only, not persisted).
// The caller is responsible for persisting the reservation to the cluster.
func (c *FailoverReservationController) scheduleAndBuildNewFailoverReservation(
	ctx context.Context,
	vm VM,
	allHypervisors []string,
	failoverReservations []v1alpha1.Reservation,
) (*v1alpha1.Reservation, error) {
	// Get potential hypervisors from scheduler
	validHypervisors, err := c.queryHypervisorsFromScheduler(ctx, vm, allHypervisors, PipelineNewFailoverReservation)
	if err != nil {
		return nil, fmt.Errorf("failed to get potential hypervisors for VM: %w", err)
	}

	// Iterate through scheduler-returned hypervisors to find one that passes eligibility constraints
	var selectedHypervisor string
	for _, candidateHypervisor := range validHypervisors {
		// Check if the VM can create a new reservation on this hypervisor
		hypotheticalRes := v1alpha1.Reservation{
			Status: v1alpha1.ReservationStatus{
				Host: candidateHypervisor,
				// Empty FailoverReservation status - new reservation has no allocations
			},
		}
		// todo we should update the API to not create a partial reservation object here
		if IsVMEligibleForReservation(vm, hypotheticalRes, failoverReservations) {
			selectedHypervisor = candidateHypervisor
			log.V(1).Info("VM can create new reservation on hypervisor",
				"vmUUID", vm.UUID,
				"hypervisor", candidateHypervisor)
			break
		}
	}

	if selectedHypervisor == "" {
		return nil, fmt.Errorf("no eligible hypervisors after constraint checking (scheduler returned %d hypervisors, all rejected)", len(validHypervisors))
	}

	log.V(1).Info("scheduler selected hypervisor for failover reservation",
		"vmUUID", vm.UUID,
		"selectedHypervisor", selectedHypervisor,
		"allReturnedHypervisors", validHypervisors)

	// Build the failover reservation on the selected hypervisor (in-memory only)
	reservation := buildFailoverReservation(vm, selectedHypervisor, c.Config.Creator)

	return reservation, nil
}

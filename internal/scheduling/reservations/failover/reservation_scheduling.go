// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"fmt"
	"slices"
	"sort"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// Pipeline names for failover reservation scheduling
const (
	// PipelineNewFailoverReservation is used to find a host for creating a new reservation.
	// Uses the general-purpose pipeline; LockReservations is set via Options.
	PipelineNewFailoverReservation = "kvm-general-purpose-load-balancing"
)

// DefaultFailoverOptions is the base scheduling.Options for all failover scheduling calls.
// Per-call overrides (ReadOnly, LockReservations) are applied on top.
var DefaultFailoverOptions = scheduling.Options{
	SkipInflight:                  true,
	SkipCommittedResourceTracking: true,
}

// InferFailoverPipeline returns the standard pipeline for a failover scheduling call based on
// the VM's flavor extra specs — the same HANA vs general-purpose split used by Nova placement.
// An unparseable flavor type falls back to the general-purpose pipeline.
func InferFailoverPipeline(extraSpecs map[string]string) string {
	flavorType, err := api.FlavorTypeFromExtraSpecs(extraSpecs)
	if err == nil && flavorType == api.FlavorTypeHANA {
		return "kvm-hana-bin-packing"
	}
	return "kvm-general-purpose-load-balancing"
}

func (c *FailoverReservationController) queryHypervisorsFromScheduler(ctx context.Context, vm reservations.VM, allHypervisors []string, pipeline string, resSpec resolvedReservationSpec, intent v1alpha1.SchedulingIntent, opts scheduling.Options) ([]string, error) {
	logger := LoggerFromContext(ctx)

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
		logger.V(1).Info("no eligible hypervisors for failover reservation",
			"vmCurrentHypervisor", vm.CurrentHypervisor)
		return nil, fmt.Errorf("no eligible hypervisors for failover reservation (VM is on %s)", vm.CurrentHypervisor)
	}

	ignoreHypervisors := []string{vm.CurrentHypervisor}

	// Build the typed scheduler request directly (in-process; no HTTP). KVM
	// failover reservations gather all candidates; the eligible list is a
	// starting set. The filter_correct_az filter excludes hosts outside the VM's
	// AZ. ImageHypervisorType is irrelevant here: filter_image_properties skips
	// the failover/reuse reservation intents.
	hosts := make([]compute.Host, 0, len(eligibleHypervisors))
	weights := make(map[string]float64, len(eligibleHypervisors))
	for _, h := range eligibleHypervisors {
		hosts = append(hosts, compute.Host{ComputeHost: h.ComputeHost, HypervisorHostname: h.HypervisorHostname})
		weights[h.ComputeHost] = 0.0
	}
	requiredTraits, forbiddenTraits := api.SplitTraits(vm.FlavorExtraSpecs)
	schedReq := compute.PlacementRequest{
		Pipeline:              pipeline,
		GatherAllCandidates:   true,
		HypervisorFamily:      api.HypervisorFamilyKVM,
		Hosts:                 hosts,
		Weights:               weights,
		Options:               opts,
		Intent:                intent,
		InstanceUUID:          vm.UUID,
		NumInstances:          1, // one reservation per VM; NumInstances must be >= 1 or capacity/bin-pack math treats the host as free
		ProjectID:             vm.ProjectID,
		DomainName:            vm.DomainName,
		AvailabilityZone:      vm.AvailabilityZone,
		IgnoreHosts:           ignoreHypervisors,
		ResourceGroup:         resSpec.ResourceGroup(vm.FlavorName),
		RequiredTraits:        requiredTraits,
		ForbiddenTraits:       forbiddenTraits,
		RequestedCapabilities: api.HypervisorTypeCapability(vm.FlavorExtraSpecs, true),
		AcceleratorsRequested: api.RequestsAccelerators(vm.FlavorExtraSpecs),
		Flavor: api.FlavorSpec{
			VCPUs:    resSpec.VCPUs,
			MemoryMB: resSpec.MemoryMB,
		},
	}

	logger.V(1).Info("scheduling failover reservation",
		"vmUUID", vm.UUID,
		"pipeline", pipeline,
		"eligibleHypervisors", len(eligibleHypervisors),
		"ignoreHypervisors", ignoreHypervisors)

	out, err := c.runScheduler(ctx, schedReq)
	if err != nil {
		logger.Error(err, "failed to schedule failover reservation", "vmUUID", vm.UUID, "pipeline", pipeline)
		return nil, fmt.Errorf("failed to schedule failover reservation: %w", err)
	}

	logger.V(1).Info("scheduler returned hypervisors for failover reservation",
		"vmUUID", vm.UUID,
		"pipeline", pipeline,
		"eligibleHypervisors", len(eligibleHypervisors),
		"ignoreHypervisors", ignoreHypervisors,
		"returnedHypervisors", out.OrderedHosts)

	return out.OrderedHosts, nil
}

// runScheduler runs the shared scheduler, choosing the lock-free path for
// read-only runs (e.g. reuse/evacuate compatibility checks) and the locked path
// for runs that mutate shared reservation state.
func (c *FailoverReservationController) runScheduler(ctx context.Context, req compute.PlacementRequest) (compute.PlacementResult, error) {
	if req.Options.ReadOnly {
		return c.Scheduler.RunWithoutLock(ctx, req, nil, nil)
	}
	return c.Scheduler.Run(ctx, req, nil, nil)
}

// tryReuseExistingReservation finds an existing reservation that can be reused for a VM.
// It returns a copy of the reservation with the VM added to its allocations (in-memory only, not persisted).
// The original reservation in the input slice is NOT modified.
// The caller is responsible for persisting the changes to the cluster.
func (c *FailoverReservationController) tryReuseExistingReservation(
	ctx context.Context,
	vm reservations.VM,
	failoverReservations []v1alpha1.Reservation,
	allHypervisors []string,
	resSpec resolvedReservationSpec,
) *v1alpha1.Reservation {

	logger := LoggerFromContext(ctx)

	reuseOpts := DefaultFailoverOptions
	reuseOpts.ReadOnly = true
	validHypervisors, err := c.queryHypervisorsFromScheduler(ctx, vm, allHypervisors, InferFailoverPipeline(vm.FlavorExtraSpecs), resSpec, api.ReuseFailoverReservationIntent, reuseOpts)
	if err != nil {
		logger.Error(err, "failed to get potential hypervisors for VM", "vmUUID", vm.UUID)
		return nil
	}
	if len(validHypervisors) == 0 {
		logger.V(1).Info("no potential hypervisors returned by scheduler for VM", "vmUUID", vm.UUID)
		return nil
	}

	eligibleReservations := FindEligibleReservations(vm, failoverReservations)
	if len(eligibleReservations) == 0 {
		logger.V(1).Info("no eligible reservations found for VM", "vmUUID", vm.UUID)
		return nil
	}

	// Sort reservations by number of allocations (prefer reservations with more VMs for better sharing)
	sort.Slice(eligibleReservations, func(i, j int) bool {
		iAllocs := len(getFailoverAllocations(&eligibleReservations[i]))
		jAllocs := len(getFailoverAllocations(&eligibleReservations[j]))
		return iAllocs > jAllocs // Descending order - more allocations first
	})

	for _, reservation := range eligibleReservations {
		logger.V(2).Info("checking existing reservation for eligibility",
			"vmUUID", vm.UUID,
			"reservationName", reservation.Name,
			"reservationHypervisor", reservation.Status.Host)
		if slices.Contains(validHypervisors, reservation.Status.Host) {
			// Create a copy of the reservation with the VM added
			updatedRes := addVMToReservation(reservation, vm)
			logger.V(1).Info("found reusable reservation for VM",
				"vmUUID", vm.UUID,
				"reservationName", updatedRes.Name,
				"hypervisor", updatedRes.Status.Host)
			return updatedRes
		}
	}

	logger.V(1).Info("no reusable reservation found for VM",
		"vmUUID", vm.UUID,
		"eligibleReservationsCount", len(eligibleReservations),
		"validHypervisorsCount", len(validHypervisors))
	return nil
}

// validateVMViaSchedulerEvacuation sends an evacuation-style scheduling request to validate
// that a VM can use the reservation host.
// TODO this is a bit of a hack. Ideally we have a special kind of request for that which would also verify that we equally are using the reservation
func (c *FailoverReservationController) validateVMViaSchedulerEvacuation(
	ctx context.Context,
	vm reservations.VM,
	reservationHost string,
) (bool, error) {

	logger := LoggerFromContext(ctx)

	// Get memory and vcpus from VM resources
	var memoryMB uint64
	var vcpus uint64
	if memory, ok := vm.Resources[string(hv1.ResourceMemory)]; ok {
		memoryMB = uint64(memory.Value() / (1024 * 1024)) //nolint:gosec // memory values won't overflow
	}
	if vcpusRes, ok := vm.Resources[string(hv1.ResourceCPU)]; ok {
		vcpus = uint64(vcpusRes.Value()) //nolint:gosec // vcpus values won't overflow
	}

	// Infer the pipeline from the VM's flavor extra specs (HANA vs general purpose).
	flavorExtraSpecs := make(map[string]string)
	for k, v := range vm.FlavorExtraSpecs {
		flavorExtraSpecs[k] = v
	}
	if _, ok := flavorExtraSpecs["capabilities:hypervisor_type"]; !ok {
		flavorExtraSpecs["capabilities:hypervisor_type"] = "qemu"
	}

	// Validate the VM can use the reservation host. Set the Evacuate intent so
	// filter_has_enough_capacity unlocks the failover reservation's resources for
	// this VM (avoiding self-blocking). Use the actual VM UUID so the filter can
	// match it in the reservation's allocations. KVM gathers all candidates; the
	// reservation host must come back ranked for the VM to be compatible.
	pipeline := InferFailoverPipeline(flavorExtraSpecs)
	requiredTraits, forbiddenTraits := api.SplitTraits(flavorExtraSpecs)
	evacuateOpts := DefaultFailoverOptions
	evacuateOpts.ReadOnly = true
	evacuateOpts.LockReservations = true
	schedReq := compute.PlacementRequest{
		Pipeline:              pipeline,
		GatherAllCandidates:   true,
		HypervisorFamily:      api.HypervisorFamilyKVM,
		Hosts:                 []compute.Host{{ComputeHost: reservationHost, HypervisorHostname: reservationHost}},
		Weights:               map[string]float64{reservationHost: 0.0},
		Options:               evacuateOpts,
		Intent:                api.EvacuateIntent,
		InstanceUUID:          vm.UUID,
		NumInstances:          1, // validating one VM; NumInstances must be >= 1 or capacity/bin-pack math treats the host as free
		ProjectID:             vm.ProjectID,
		DomainName:            vm.DomainName,
		AvailabilityZone:      vm.AvailabilityZone,
		IgnoreHosts:           []string{vm.CurrentHypervisor},
		RequiredTraits:        requiredTraits,
		ForbiddenTraits:       forbiddenTraits,
		RequestedCapabilities: api.HypervisorTypeCapability(flavorExtraSpecs, true),
		AcceleratorsRequested: api.RequestsAccelerators(flavorExtraSpecs),
		Flavor: api.FlavorSpec{
			VCPUs:    vcpus,
			MemoryMB: memoryMB,
		},
	}

	logger.V(1).Info("validating VM via scheduler evacuation",
		"vmUUID", vm.UUID,
		"reservationHost", reservationHost,
		"vmCurrentHost", vm.CurrentHypervisor,
		"pipeline", pipeline)

	out, err := c.runScheduler(ctx, schedReq)
	if err != nil {
		logger.Error(err, "failed to validate VM for reservation host", "vmUUID", vm.UUID, "reservationHost", reservationHost)
		return false, fmt.Errorf("failed to validate VM for reservation host: %w", err)
	}
	hosts := out.OrderedHosts

	// Handle empty response - no hosts returned
	if len(hosts) < 1 {
		logger.V(1).Info("scheduler returned no hosts for VM validation", "vmUUID", vm.UUID, "reservationHost", reservationHost)
		return false, nil
	}

	// The reservation host must be among the returned hosts for the VM to be
	// compatible with it.
	return slices.Contains(hosts, reservationHost), nil
}

// scheduleAndBuildNewFailoverReservation schedules a failover reservation for a VM.
// Returns the built reservation (in-memory only, not persisted).
// The caller is responsible for persisting the reservation to the cluster.
// excludeHypervisors is a set of hypervisors to exclude from consideration (e.g., hypervisors
// that already had a new reservation created in this reconcile cycle).
func (c *FailoverReservationController) scheduleAndBuildNewFailoverReservation(
	ctx context.Context,
	vm reservations.VM,
	allHypervisors []string,
	failoverReservations []v1alpha1.Reservation,
	excludeHypervisors map[string]bool,
	resSpec resolvedReservationSpec,
) (*v1alpha1.Reservation, error) {

	logger := LoggerFromContext(ctx)

	// Get potential hypervisors from scheduler using the reservation spec resources
	// (which may be sized to the LargestFlavor from the flavor group)
	newResOpts := DefaultFailoverOptions
	newResOpts.LockReservations = true
	validHypervisors, err := c.queryHypervisorsFromScheduler(ctx, vm, allHypervisors, PipelineNewFailoverReservation, resSpec, api.ReserveForFailoverIntent, newResOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get potential hypervisors for VM: %w", err)
	}

	// Iterate through scheduler-returned hypervisors to find one that passes eligibility constraints
	var selectedHypervisor string
	for _, candidateHypervisor := range validHypervisors {
		// Skip hypervisors that already had a new reservation created in this reconcile cycle
		if c.Config.LimitOneNewReservationPerHypervisor && excludeHypervisors[candidateHypervisor] {
			logger.V(1).Info("skipping hypervisor (already has new reservation this cycle)",
				"vmUUID", vm.UUID, "hypervisor", candidateHypervisor)
			continue
		}
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
			logger.V(1).Info("VM can create new reservation on hypervisor", "vmUUID", vm.UUID, "hypervisor", candidateHypervisor)
			break
		}
	}

	if selectedHypervisor == "" {
		logger.V(1).Info("no eligible hypervisors after constraint checking", "vmUUID", vm.UUID, "schedulerReturnedCount", len(validHypervisors))
		return nil, fmt.Errorf("no eligible hypervisors after constraint checking (scheduler returned %d hypervisors, all rejected)", len(validHypervisors))
	}

	logger.V(1).Info("scheduler selected hypervisor for failover reservation",
		"vmUUID", vm.UUID,
		"selectedHypervisor", selectedHypervisor,
		"allReturnedHypervisors", validHypervisors)

	// Build the failover reservation using the same reservation spec resources
	reservation := newFailoverReservation(ctx, vm, selectedHypervisor, c.Config.Creator, resSpec)

	return reservation, nil
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"slices"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib/capacity"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type FilterHasEnoughCapacityOpts struct {
	// If reserved space should be locked even for matching requests.
	LockReserved bool `json:"lockReserved"`

	// IgnoredReservationTypes is a list of reservation types to ignore when calculating capacity.
	// Valid values: "CommittedResourceReservation", "FailoverReservation"
	// When a reservation type is in this list, its capacity is not blocked.
	// Default: empty (all reservation types are considered)
	IgnoredReservationTypes []v1alpha1.ReservationType `json:"ignoredReservationTypes,omitempty"`

	// IgnoreAllocations skips subtracting current VM allocations from host capacity.
	// When true, only raw hardware capacity is considered (empty datacenter scenario).
	IgnoreAllocations bool `json:"ignoreAllocations,omitempty"`
}

func (FilterHasEnoughCapacityOpts) Validate() error { return nil }

type FilterHasEnoughCapacity struct {
	lib.BaseFilter[compute.PlacementRequest, FilterHasEnoughCapacityOpts]
}

// Filter hosts that don't have enough capacity to run the requested flavor.
//
// This filter takes the capacity of the hosts and subtracts from it:
//   - The resources currently used by VMs.
//   - The resources reserved by active Reservations.
//
// In case the project and flavor match, space reserved is unlocked (slotting).
//
// Capacity accounting uses two sources: hv.Status.Allocation (aggregate real-time usage of
// all running VMs) and Reservation.Status.Allocations (which VMs are confirmed on a slot,
// maintained by the reservation controller with a one-reconcile-cycle lag). During the window
// between a VM starting and the reservation controller reconciling, a VM appears in both
// sources — a conservative transient over-count that self-corrects on the next reconcile.
//
// During a CR reservation migration (TargetHost != Status.Host), both the source and target
// host are blocked with the full slot. The source block is intentionally conservative to
// preserve rollback capacity if the migration fails.
//
// Please note that, if num_instances is larger than 1, there needs to be enough
// capacity to place all instances on the same host. This limitation is necessary
// because we can't spread out instances, as the final set of valid hosts is not
// known at this point.
//
// Please also note that disk space is currently not considered by this filter.
func (s *FilterHasEnoughCapacity) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	opts := request.GetOptions()
	result := s.IncludeAllHostsFromRequest(request)

	// Merge call-time options with static step config.
	ignoreAllocations := s.Options.IgnoreAllocations || opts.AssumeEmptyHosts
	ignoredReservationTypes := slices.Concat(s.Options.IgnoredReservationTypes, opts.IgnoredReservationTypes)

	// Collect the candidate hypervisors for this request.
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}

	// Decide, per reservation, whether it should block capacity for THIS request.
	// The request-aware unlock/slotting logic (CR project+flavor match, failover
	// evacuation/reuse, not-ready double-booking) lives here; the resulting set of
	// blocking reservations is then fed to the shared capacity math, which performs
	// the effective-capacity / allocation / reservation subtraction identically for
	// every consumer.
	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}
	reservationsToBlock := make([]v1alpha1.Reservation, 0, len(reservations.Items))
	for _, reservation := range reservations.Items {
		// Check if this reservation type should be ignored — applies regardless of ready state.
		if slices.Contains(ignoredReservationTypes, reservation.Spec.Type) {
			traceLog.V(1).Info("ignoring reservation type", "type", reservation.Spec.Type, "reservation", reservation.Name)
			continue
		}

		if !reservation.IsReady() {
			if reservation.Spec.TargetHost == "" && reservation.Status.Host == "" {
				continue // not placed yet, nothing to block
			}
			// TargetHost is set but Ready has not been synced yet: the reservation controller
			// wrote the host in reconcile cycle 1 but hasn't completed cycle 2 (status sync).
			// Block the full slot now to prevent a concurrent scheduling call from picking the
			// same host. Unlock logic is intentionally skipped — an unconfirmed slot must not
			// be unlocked for any VM or project.
			traceLog.V(1).Info("reservation has target host set but is not yet ready — blocking host to prevent double-booking",
				"reservation", reservation.Name,
				"targetHost", reservation.Spec.TargetHost,
				"statusHost", reservation.Status.Host,
			)
		} else {
			// Ready reservation: apply type-specific unlock logic.
			switch reservation.Spec.Type {
			case v1alpha1.ReservationTypeCommittedResource, "": // Empty string for backward compatibility
				// Skip if no CommittedResourceReservation spec or no resource group set.
				if reservation.Spec.CommittedResourceReservation == nil || reservation.Spec.CommittedResourceReservation.ResourceGroup == "" {
					continue // Not handled by us (no resource group set).
				}

				// Check if this is a CR reservation scheduling request.
				// If so, we should NOT unlock any CR reservations to prevent overbooking.
				// CR capacity should only be unlocked for actual VM scheduling.
				switch {
				case request.Intent == api.ReserveForCommittedResourceIntent:
					traceLog.V(1).Info("keeping CR reservation locked for CR reservation scheduling",
						"reservation", reservation.Name,
						"intent", request.Intent)
					// Don't continue - fall through to block the resources
				case !s.Options.LockReserved && !opts.LockReservations &&
					// For committed resource reservations: unlock resources only if:
					// 1. Project ID matches
					// 2. ResourceGroup matches the flavor's hw_version
					reservation.Spec.CommittedResourceReservation.MatchesGroup(request.ProjectID, request.HardwareVersion):
					traceLog.V(1).Info("unlocking resources reserved by matching committed resource reservation with allocation",
						"reservation", reservation.Name,
						"instanceUUID", request.InstanceUUID,
						"projectID", request.ProjectID,
						"resourceGroup", reservation.Spec.CommittedResourceReservation.ResourceGroup)
					continue
				}

			case v1alpha1.ReservationTypeFailover:
				// For failover reservations: if the requested VM is contained in the allocations map
				// AND this is an evacuation request, unlock the resources.
				// We only unlock during evacuations because:
				// 1. Failover reservations are specifically for HA/evacuation scenarios.
				// 2. During live migrations or other operations, we don't want to use failover capacity.
				// Note: we cannot use failover reservations from other VMs, as that can invalidate our HA guarantees.
				switch request.Intent {
				case api.EvacuateIntent:
					if reservation.Status.FailoverReservation != nil {
						if _, contained := reservation.Status.FailoverReservation.Allocations[request.InstanceUUID]; contained {
							traceLog.V(1).Info("unlocking resources reserved by failover reservation for VM in allocations (evacuation)",
								"reservation", reservation.Name,
								"instanceUUID", request.InstanceUUID)
							continue
						}
					}
				case api.ReuseFailoverReservationIntent:
					// Reuse check: the reservation already pre-blocks the right capacity for this VM.
					// Don't subtract it from free capacity to avoid double-counting.
					traceLog.V(1).Info("skipping failover reservation block for reuse compatibility check",
						"reservation", reservation.Name)
					continue
				}
				traceLog.V(1).Info("processing failover reservation", "reservation", reservation.Name)
			}
		}

		if reservation.Spec.TargetHost == "" && reservation.Status.Host == "" {
			traceLog.V(1).Info("skipping reservation with no host", "reservation", reservation.Name)
			continue
		}
		reservationsToBlock = append(reservationsToBlock, reservation)
	}

	// Compute usage per candidate host via the shared capacity math.
	// DescribeHostUsage starts from EffectiveCapacity (falling back to Capacity), subtracts
	// allocation (unless ignored), and subtracts each surviving reservation's block on
	// both its TargetHost and Status.Host, flooring at zero.
	usageByHost := make(map[string]capacity.HostUsage)
	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		if hv.Status.EffectiveCapacity == nil {
			traceLog.V(1).Info("hypervisor with nil effective capacity, use capacity instead (overprovisioning not considered)", "host", hv.Name)
		}
		usage, err := capacity.DescribeHostUsage(hv, reservationsToBlock, capacity.Options{
			IgnoreAllocations:         ignoreAllocations,
			BlockUnplacedReservations: true,
			ClampToZero:               true,
		})
		if err != nil {
			continue // no capacity data; dropped below as an un-encountered host
		}
		usageByHost[hv.Name] = usage
	}

	// A host must fit the full request: flavor resources × NumInstances. We can't spread
	// instances across hosts, so all requested instances must land on the same host.
	if request.Flavor.VCPUs == 0 {
		return nil, errors.New("flavor has 0 vcpus")
	}
	if request.Flavor.MemoryMB == 0 {
		return nil, errors.New("flavor has 0 memory")
	}
	// Nova's memory_mb field is named "megabytes" in the API docs, but the values are in
	// mebibytes (1 MiB = 2^20 bytes). Multiply by 1<<20 to convert to the bytes used in
	// hv.Status.
	//nolint:gosec // flavor fields are small, non-negative counts.
	required := map[hv1.ResourceName]resource.Quantity{
		"cpu":    *resource.NewQuantity(int64(request.Flavor.VCPUs*request.NumInstances), resource.DecimalSI),
		"memory": *resource.NewQuantity(int64(request.Flavor.MemoryMB*request.NumInstances*(1<<20)), resource.BinarySI),
	}

	hostsEncountered := make(map[string]struct{})
	for host, usage := range usageByHost {
		hostsEncountered[host] = struct{}{}

		freeCPU := usage.Free["cpu"]
		freeMemory := usage.Free["memory"]
		if !usage.Fits(required) {
			traceLog.V(1).Info(
				"filtering host due to insufficient capacity",
				"host", host,
				"requested_cpus", request.Flavor.VCPUs,
				"requested_memory_mb", request.Flavor.MemoryMB,
				"num_instances", request.NumInstances,
				"available_cpus", freeCPU.String(),
				"available_memory", freeMemory.String(),
			)
			delete(result.Activations, host)
			continue
		}
		traceLog.V(1).Info(
			"host has enough capacity", "host", host,
			"requested_cpus", request.Flavor.VCPUs,
			"available_cpus", freeCPU.String(),
			"requested_memory_mb", request.Flavor.MemoryMB,
			"available_memory", freeMemory.String(),
		)
	}

	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.V(1).Info(
				"removing host with unknown capacity",
				"host", host,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_has_enough_capacity"] = func() NovaFilter { return &FilterHasEnoughCapacity{} }
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/capacity"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterHasEnoughCapacityOpts struct {
	// If reserved space should be locked even for matching requests.
	LockReserved bool `json:"lockReserved"`
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

	// Call-time options plus static step config.
	assumeEmptyHosts := opts.AssumeEmptyHosts
	lockReserved := s.Options.LockReserved || opts.LockReservations

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}

	// Which reservations block capacity for THIS request. This is the only
	// request-dependent, Nova-specific policy; pkg/capacity does the arithmetic once
	// the active list is known. A reservation is dropped (its capacity handed to the
	// request) when the request is entitled to it: a matching committed-resource slot,
	// a failover slot being evacuated/reused by this very VM, or a failover-host
	// reservation (handled by the dedicated filter_failover_host). Everything else blocks.
	//
	// The empty-datacenter view (assumeEmptyHosts) drops every reservation unconditionally:
	// it asks how much a host could hold once fully cleared, so neither running workloads
	// nor any reservation count. The reservation list is therefore left empty, and the fit
	// check below runs against Total rather than Available.
	var active []v1alpha1.Reservation
	if !assumeEmptyHosts {
		var reservations v1alpha1.ReservationList
		if err := s.Client.List(context.Background(), &reservations); err != nil {
			return nil, err
		}
		active = make([]v1alpha1.Reservation, 0, len(reservations.Items))
		for _, reservation := range reservations.Items {
			if s.reservationBlocks(traceLog, request, reservation, lockReserved) {
				active = append(active, reservation)
			}
		}
	}

	for i := range hvs.Items {
		hv := hvs.Items[i]
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		rep := capacity.NewHostUsageReport(hv, active)
		if rep.Total == nil {
			traceLog.V(1).Info("removing host with unknown capacity", "host", hv.Name)
			delete(result.Activations, hv.Name)
			continue
		}
		// Normal view fits against Available (Total − Used − Reserved). The empty-datacenter
		// view asks what a host could hold once fully cleared, so it fits against Total,
		// ignoring running workloads and reservations.
		fitAgainst := rep.Available
		if assumeEmptyHosts {
			fitAgainst = rep.Total
		}
		// Demand fits when every requested resource has at least that much available. A
		// non-positive demand imposes no constraint; a resource missing from the host fails.
		demand := request.TotalCapacityDemand()
		fits := true
		for name, need := range demand {
			if need.Sign() <= 0 {
				continue
			}
			have, ok := fitAgainst[name]
			if !ok || have.Cmp(need) < 0 {
				fits = false
				break
			}
		}
		if !fits {
			traceLog.V(1).Info("filtering host due to insufficient capacity",
				"host", hv.Name,
				"available", fitAgainst,
				"requested", demand,
			)
			delete(result.Activations, hv.Name)
			continue
		}
		traceLog.V(1).Info("host has enough capacity",
			"host", hv.Name,
			"available", fitAgainst,
			"requested", demand,
		)
	}

	// Remove any candidate for which we saw no hypervisor at all.
	seen := make(map[string]struct{}, len(hvs.Items))
	for i := range hvs.Items {
		seen[hvs.Items[i].Name] = struct{}{}
	}
	for host := range result.Activations {
		if _, ok := seen[host]; !ok {
			traceLog.V(1).Info("removing host with unknown capacity", "host", host)
			delete(result.Activations, host)
		}
	}
	return result, nil
}

// reservationBlocks reports whether reservation should count against free capacity for
// this request. It returns false (unlock) only when the request is specifically
// entitled to the reservation's capacity; otherwise the reservation blocks.
//
// The decision mirrors the historical per-type logic:
//   - An unconfirmed-but-placed reservation (TargetHost/Status.Host set, not Ready) always
//     blocks — an unconfirmed slot must not be unlocked for anyone.
//   - A committed-resource slot unlocks for a normal placement whose project and
//     hardware version match the slot (slotting), unless LockReserved is set or the
//     request is itself reserving committed-resource capacity (which must not overbook).
//   - A failover slot unlocks only for the VM it holds, and only during an evacuation of
//     that VM, or for a failover-reuse compatibility check.
func (s *FilterHasEnoughCapacity) reservationBlocks(
	traceLog logr.Logger,
	request compute.PlacementRequest,
	reservation v1alpha1.Reservation,
	lockReserved bool,
) bool {

	// Not placed yet: nothing to block. Placed-but-not-ready: always block.
	if !reservation.IsReady() {
		return reservation.Spec.TargetHost != "" || reservation.Status.Host != ""
	}

	switch reservation.Spec.Type {
	case v1alpha1.ReservationTypeCommittedResource, "": // "" kept for backward compatibility
		if reservation.Spec.CommittedResourceReservation == nil || reservation.Spec.CommittedResourceReservation.ResourceGroup == "" {
			return false // not handled by us (no resource group set)
		}
		if request.Intent == api.ReserveForCommittedResourceIntent {
			// Reserving CR capacity must not unlock other CR slots (would overbook).
			return true
		}
		if !lockReserved && reservation.Spec.CommittedResourceReservation.MatchesGroup(request.ProjectID, request.HardwareVersion) {
			traceLog.V(1).Info("unlocking committed resource reservation for matching project and flavor group",
				"reservation", reservation.Name, "projectID", request.ProjectID)
			return false
		}
		return true

	case v1alpha1.ReservationTypeFailover:
		switch request.Intent {
		case api.EvacuateIntent:
			if reservation.Status.FailoverReservation != nil {
				if _, contained := reservation.Status.FailoverReservation.Allocations[request.InstanceUUID]; contained {
					traceLog.V(1).Info("unlocking failover reservation for VM being evacuated",
						"reservation", reservation.Name, "instanceUUID", request.InstanceUUID)
					return false
				}
			}
		case api.ReuseFailoverReservationIntent:
			// The reservation already pre-blocks this VM's capacity; don't double-count.
			return false
		}
		return true
	case v1alpha1.ReservationTypeFailoverHost:
		// Failover-host reservations are handled by the dedicated filter, so this filter does not block them.
		return false
	}
	return true
}

func init() {
	Index["filter_has_enough_capacity"] = func() NovaFilter { return &FilterHasEnoughCapacity{} }
}

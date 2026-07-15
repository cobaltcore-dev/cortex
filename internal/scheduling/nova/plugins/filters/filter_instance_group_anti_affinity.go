// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// FilterInstanceGroupAntiAffinityOpts controls whether the filter also
// considers pending failover reservations when evaluating anti-affinity.
//
// The failover reservation status carries a map of VMs that would land on a
// host during evacuation. Without these flags those VMs are invisible to
// anti-affinity accounting, which can result in co-location after a failover.
type FilterInstanceGroupAntiAffinityOpts struct {
	// FailoverPlacementConsidersFailoverReservation, when true, counts VMs
	// with an existing failover allocation on a host toward
	// max_server_per_host when the request is placing a new failover
	// reservation (detected via ExternalSchedulerRequest.GetIntent() ==
	// ReserveForFailoverIntent).
	FailoverPlacementConsidersFailoverReservation bool `json:"failoverPlacementConsidersFailoverReservation,omitempty"`

	// VMPlacementConsidersFailoverReservation, when true, counts VMs with
	// an existing failover allocation on a host toward max_server_per_host
	// when the request is placing a regular (non-failover) VM.
	VMPlacementConsidersFailoverReservation bool `json:"vmPlacementConsidersFailoverReservation,omitempty"`
}

func (FilterInstanceGroupAntiAffinityOpts) Validate() error { return nil }

type FilterInstanceGroupAntiAffinityStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterInstanceGroupAntiAffinityOpts]
}

// Select hosts not in spec_obj.instance_group but only until
// max_server_per_host is reached (by default = 1).
//
// When the appropriate option flag is set, the filter also treats VMs that
// have a failover allocation targeted at a host (via
// Reservation.Status.FailoverReservation.Allocations on reservations of type
// FailoverReservation with Status.Host == candidate) as occupancy for
// anti-affinity accounting.
func (s *FilterInstanceGroupAntiAffinityStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	// Detect the request's intent up front. Instance-group hints are
	// typically only present in user-initiated VM placement requests, but
	// the failover reservation controller can forward an instance group so
	// that anti-affinity is honored when placing a new failover slot.
	intent, intentErr := request.GetIntent()
	isFailoverPlacement := intentErr == nil && intent == api.ReserveForFailoverIntent

	// Skip this filter entirely for intents that never carry meaningful
	// instance-group data. Failover placement is a special case: we only
	// skip if the operator has not opted into considering failover
	// allocations on that path. Failover reuse deliberately does NOT skip
	// here: peers of a group must not co-locate on the same failover slot
	// during evacuation, so the filter must run and enforce anti-affinity
	// even when the caller is validating a reuse candidate.
	if intentErr == nil && slices.Contains([]v1alpha1.SchedulingIntent{
		api.ReserveForCommittedResourceIntent,
		api.CapacityProbeIntent,
	}, intent) {
		return result, nil
	}
	if isFailoverPlacement && !s.Options.FailoverPlacementConsidersFailoverReservation {
		return result, nil
	}

	ig := request.Spec.Data.InstanceGroup
	if ig == nil {
		traceLog.Info("no instance group in request, skipping filter")
		return result, nil
	}
	policy := ig.Data.Policy
	if policy != "anti-affinity" {
		traceLog.Info("instance group policy is not 'anti-affinity', skipping filter")
		return result, nil
	}
	memberVMs := ig.Data.Members
	if len(memberVMs) == 0 {
		// Nothing to do.
		traceLog.Info("instance group has no members, skipping filter")
		return result, nil
	}
	maxServersPerHost := 1
	if ig.Data.Rules != nil {
		if maxServersPerHostAny, ok := ig.Data.Rules["max_server_per_host"]; ok {
			if maxServersPerHostInt, ok := maxServersPerHostAny.(int); ok {
				maxServersPerHost = maxServersPerHostInt
			}
		}
	}

	considerFailover := (isFailoverPlacement && s.Options.FailoverPlacementConsidersFailoverReservation) ||
		(!isFailoverPlacement && s.Options.VMPlacementConsidersFailoverReservation)

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}

	// Build a map from target host -> set of VM UUIDs that have a failover
	// allocation targeted at that host. Only failover reservations contribute.
	failoverVMsByHost := map[string]map[string]struct{}{}
	if considerFailover {
		reservations := &v1alpha1.ReservationList{}
		if err := s.Client.List(context.Background(), reservations); err != nil {
			// Non-fatal: fall back to running-instances-only anti-affinity.
			// We prefer a possibly-permissive result over failing the
			// entire scheduling request when the reservation cache is
			// temporarily unavailable.
			traceLog.Warn(
				"failed to list reservations, skipping failover-allocation accounting",
				"error", err,
			)
		} else {
			for _, r := range reservations.Items {
				if r.Spec.Type != v1alpha1.ReservationTypeFailover {
					continue
				}
				if r.Status.Host == "" || r.Status.FailoverReservation == nil {
					continue
				}
				for vmUUID := range r.Status.FailoverReservation.Allocations {
					set, ok := failoverVMsByHost[r.Status.Host]
					if !ok {
						set = map[string]struct{}{}
						failoverVMsByHost[r.Status.Host] = set
					}
					set[vmUUID] = struct{}{}
				}
			}
		}
	}

	for host := range result.Activations {
		hv, ok := hvsByName[host]
		if !ok {
			traceLog.Error("hypervisor not found for host", "host", host)
			delete(result.Activations, host)
			continue
		}
		// Check if this host is already running the same vm (resizes).
		// In this case we should not filter it out.
		if slices.ContainsFunc(hv.Status.Instances, func(inst hv1.Instance) bool {
			return inst.ID == request.Spec.Data.InstanceUUID
		}) {
			traceLog.Info("host is running the same VM, not filtering out", "host", host)
			continue
		}
		// Failover self-identity: if the VM being placed already has a
		// failover allocation targeted at this host, do not self-filter.
		if considerFailover {
			if _, self := failoverVMsByHost[host][request.Spec.Data.InstanceUUID]; self {
				traceLog.Info(
					"host already has a failover allocation for the same VM, not filtering out",
					"host", host,
				)
				continue
			}
		}
		// Build a deduplicated set of same-group VM UUIDs on this host,
		// combining running instances and (optionally) failover allocations.
		instancesInGroup := map[string]struct{}{}
		for _, inst := range hv.Status.Instances {
			if inst.ID == request.Spec.Data.InstanceUUID {
				continue
			}
			if slices.Contains(memberVMs, inst.ID) {
				instancesInGroup[inst.ID] = struct{}{}
			}
		}
		if considerFailover {
			for vmUUID := range failoverVMsByHost[host] {
				if vmUUID == request.Spec.Data.InstanceUUID {
					continue
				}
				if slices.Contains(memberVMs, vmUUID) {
					instancesInGroup[vmUUID] = struct{}{}
				}
			}
		}
		if len(instancesInGroup) >= maxServersPerHost {
			delete(result.Activations, host)
			traceLog.Info(
				"filtered out host exceeding max_server_per_host for instance group",
				"host", host,
				"instances_in_group_count", len(instancesInGroup),
				"max_server_per_host", maxServersPerHost,
				"considered_failover", considerFailover,
				"is_failover_placement", isFailoverPlacement,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_instance_group_anti_affinity"] = func() NovaFilter { return &FilterInstanceGroupAntiAffinityStep{} }
}

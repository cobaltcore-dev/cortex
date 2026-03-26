// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FilterCommittedResourceBookkeepingOpts struct {
	// UpdateReservationAllocations enables adding VMs to CR reservation spec allocations
	// when a matching reservation's host is among the candidates.
	// This tracks which VMs are expected to land on which reservations.
	// Default: false
	UpdateReservationAllocations bool `json:"updateReservationAllocations,omitempty"`

	// EnforceReservationSlots controls whether candidates should be filtered to only
	// hosts with available CR reservation slots when enough slots exist.
	// When true and sufficient reservation slots are available among candidates,
	// non-reservation hosts are filtered out.
	// Default: false (not yet implemented)
	EnforceReservationSlots bool `json:"enforceReservationSlots,omitempty"`
}

func (FilterCommittedResourceBookkeepingOpts) Validate() error { return nil }

type FilterCommittedResourceBookkeeping struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterCommittedResourceBookkeepingOpts]
}

// Filter for committed resource reservation bookkeeping.
//
// Note: Unlocking of CR reservation capacity happens in filter_has_enough_capacity
// when project ID and resource group (hw_version) match. This filter handles
// additional bookkeeping tasks:
//
//  1. UpdateReservationAllocations: Adds VMs to matching reservation spec allocations
//     to track which VMs are expected to use which reservations
//  2. EnforceReservationSlots: (Future) Filters candidates to reservation hosts
//     when sufficient slots exist
//
// This filter should run AFTER filter_has_enough_capacity to ensure candidates
// have already been filtered based on physical capacity.
func (s *FilterCommittedResourceBookkeeping) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// Skip if no features enabled
	if !s.Options.UpdateReservationAllocations && !s.Options.EnforceReservationSlots {
		return result, nil
	}

	// Get request details
	projectID := request.Spec.Data.ProjectID
	resourceGroup := request.Spec.Data.Flavor.Data.ExtraSpecs["hw_version"]
	instanceUUID := request.Spec.Data.InstanceUUID

	if projectID == "" || resourceGroup == "" {
		traceLog.Debug("skipping CR reservation handling: missing projectID or resourceGroup")
		return result, nil
	}

	// List all reservations
	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}

	// Find matching CR reservations with spare capacity
	var matchingReservations []v1alpha1.Reservation
	for _, reservation := range reservations.Items {
		if !s.isMatchingCRReservation(traceLog, reservation, projectID, resourceGroup, request) {
			continue
		}
		matchingReservations = append(matchingReservations, reservation)
	}

	traceLog.Debug("found matching CR reservations",
		"count", len(matchingReservations),
		"projectID", projectID,
		"resourceGroup", resourceGroup)

	// Update reservation allocations if enabled
	if s.Options.UpdateReservationAllocations && len(matchingReservations) > 0 && instanceUUID != "" {
		s.updateReservationAllocations(traceLog, request, result.Activations, matchingReservations)
	}

	// TODO: Implement EnforceReservationSlots logic
	// When enabled, filter candidates to only hosts with reservation slots if sufficient slots exist

	return result, nil
}

// isMatchingCRReservation checks if a reservation is a matching CR reservation with spare capacity.
func (s *FilterCommittedResourceBookkeeping) isMatchingCRReservation(
	traceLog *slog.Logger,
	reservation v1alpha1.Reservation,
	projectID, resourceGroup string,
	request api.ExternalSchedulerRequest,
) bool {
	// Must be Ready
	if !meta.IsStatusConditionTrue(reservation.Status.Conditions, v1alpha1.ReservationConditionReady) {
		return false
	}

	// Must be a CR reservation
	if reservation.Spec.Type != v1alpha1.ReservationTypeCommittedResource {
		return false
	}

	// Must have CR spec
	if reservation.Spec.CommittedResourceReservation == nil {
		return false
	}

	// Must match project and resource group
	if reservation.Spec.CommittedResourceReservation.ProjectID != projectID {
		return false
	}
	if reservation.Spec.CommittedResourceReservation.ResourceGroup != resourceGroup {
		return false
	}

	// Must have a host
	if reservation.Spec.TargetHost == "" && reservation.Status.Host == "" {
		return false
	}

	// Must have spare capacity
	if !s.hasSpareCapacity(traceLog, reservation, request) {
		return false
	}

	return true
}

// hasSpareCapacity checks if the reservation has enough spare capacity for the VM.
func (s *FilterCommittedResourceBookkeeping) hasSpareCapacity(
	traceLog *slog.Logger,
	reservation v1alpha1.Reservation,
	request api.ExternalSchedulerRequest,
) bool {
	// Calculate current usage from existing allocations
	var usedCPU, usedMemory int64
	if reservation.Spec.CommittedResourceReservation != nil {
		for _, allocation := range reservation.Spec.CommittedResourceReservation.Allocations {
			if cpu, ok := allocation.Resources["cpu"]; ok {
				usedCPU += cpu.Value()
			}
			if memory, ok := allocation.Resources["memory"]; ok {
				usedMemory += memory.Value()
			}
		}
	}

	// Get reservation's total capacity
	var totalCPU, totalMemory int64
	if cpu, ok := reservation.Spec.Resources["cpu"]; ok {
		totalCPU = cpu.Value()
	}
	if memory, ok := reservation.Spec.Resources["memory"]; ok {
		totalMemory = memory.Value()
	}

	// Calculate requested resources
	//nolint:gosec // VCPUs and MemoryMB are bounded by OpenStack limits, no overflow risk
	requestedCPU := int64(request.Spec.Data.Flavor.Data.VCPUs)
	//nolint:gosec // VCPUs and MemoryMB are bounded by OpenStack limits, no overflow risk
	requestedMemory := int64(request.Spec.Data.Flavor.Data.MemoryMB) * 1_000_000 // Convert MB to bytes

	// Check if there's enough spare capacity
	spareCPU := totalCPU - usedCPU
	spareMemory := totalMemory - usedMemory

	if spareCPU < requestedCPU || spareMemory < requestedMemory {
		traceLog.Debug("reservation has insufficient spare capacity",
			"reservation", reservation.Name,
			"spareCPU", spareCPU,
			"spareMemory", spareMemory,
			"requestedCPU", requestedCPU,
			"requestedMemory", requestedMemory)
		return false
	}

	return true
}

// updateReservationAllocations adds the VM to the spec allocations of matching CR reservations
// whose host is among the candidates.
func (s *FilterCommittedResourceBookkeeping) updateReservationAllocations(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
	candidates map[string]float64,
	matchingReservations []v1alpha1.Reservation,
) {

	instanceUUID := request.Spec.Data.InstanceUUID
	if instanceUUID == "" {
		traceLog.Warn("skipping reservation allocation update: no instance UUID in request")
		return
	}

	// Build resources from flavor
	//nolint:gosec // VCPUs and MemoryMB are bounded by OpenStack limits, no overflow risk
	vmResources := map[hv1.ResourceName]resource.Quantity{
		"cpu":    *resource.NewQuantity(int64(request.Spec.Data.Flavor.Data.VCPUs), resource.DecimalSI),
		"memory": *resource.NewQuantity(int64(request.Spec.Data.Flavor.Data.MemoryMB)*1_000_000, resource.DecimalSI),
	}

	now := metav1.NewTime(time.Now())

	for _, reservation := range matchingReservations {
		// Get reservation host
		reservationHost := reservation.Spec.TargetHost
		if reservationHost == "" {
			reservationHost = reservation.Status.Host
		}

		// Check if reservation's host is among candidates
		if _, isCandidate := candidates[reservationHost]; !isCandidate {
			traceLog.Debug("skipping reservation allocation: host not among candidates",
				"reservation", reservation.Name,
				"host", reservationHost)
			continue
		}

		// Check if VM is already in allocations
		if reservation.Spec.CommittedResourceReservation.Allocations != nil {
			if _, exists := reservation.Spec.CommittedResourceReservation.Allocations[instanceUUID]; exists {
				traceLog.Debug("VM already in reservation allocations",
					"reservation", reservation.Name,
					"instanceUUID", instanceUUID)
				continue
			}
		}

		// Add VM to reservation allocations
		reservationCopy := reservation.DeepCopy()
		if reservationCopy.Spec.CommittedResourceReservation.Allocations == nil {
			reservationCopy.Spec.CommittedResourceReservation.Allocations = make(map[string]v1alpha1.CommittedResourceAllocation)
		}
		reservationCopy.Spec.CommittedResourceReservation.Allocations[instanceUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: now,
			Resources:         vmResources,
		}

		if err := s.Client.Update(context.Background(), reservationCopy); err != nil {
			traceLog.Warn("failed to update reservation with VM allocation",
				"reservation", reservation.Name,
				"instanceUUID", instanceUUID,
				"error", err)
			// Continue with other reservations - this is best-effort
		} else {
			traceLog.Info("added VM to CR reservation spec allocations",
				"reservation", reservation.Name,
				"instanceUUID", instanceUUID,
				"host", reservationHost)
		}
	}
}

func init() {
	Index["filter_committed_resource_bookkeeping"] = func() NovaFilter { return &FilterCommittedResourceBookkeeping{} }
}

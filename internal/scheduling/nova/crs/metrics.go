// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crs

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"
)

// NewNoHostFoundCounter creates the Prometheus counter for no-host-found classification.
// Register it with the metrics registry before assigning it to the Recorder.
func NewNoHostFoundCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_nova_no_host_found_total",
		Help: "Nova no-host-found results classified by committed resource coverage (no_cr/cr_exhausted/slot_exhausted/slot_blocked/error).",
	}, []string{"cr_slot", "flavor_group", "intent"})
}

// NewPlacementCounter creates the Prometheus counter for successful Nova placements.
// Labels: flavor_group, intent, cr_slot (no_cr/slot_missed/slot_used/error). PAYG placements
// (flavor not in any group) are not counted — they return before reaching this counter.
// cr_slot=error is emitted when the flavor group lookup fails due to a K8s error.
// Register it with the metrics registry before assigning it to the Recorder.
func NewPlacementCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_nova_placement_total",
		Help: "Successful Nova placements by committed resource slot outcome.",
	}, []string{"flavor_group", "intent", "cr_slot"})
}

// ClassifyNoHostFound determines why no host was found for a nova placement request,
// in terms of committed resource coverage:
//
//   - no_cr: project has no active CommittedResources for the flavor group (NH1)
//   - cr_exhausted: CommittedResources exist but are fully occupied (NH2)
//   - slot_exhausted: CR has remaining capacity but no input host has a usable slot (NH3)
//   - slot_blocked: a usable slot exists but scheduling constraints excluded all such hosts (NH4)
func ClassifyNoHostFound(
	activeCRs []v1alpha1.CommittedResource,
	evaluator *SlotEvaluator,
	inputHosts []string,
	projectID, flavorGroupName string,
	vmMemBytes int64,
) string {

	if len(activeCRs) == 0 {
		return "no_cr"
	}

	totalCapacity := resource.Quantity{}
	totalUsed := resource.Quantity{}
	for _, cr := range activeCRs {
		totalCapacity.Add(cr.Spec.Amount)
		if used, ok := cr.Status.UsedResources["memory"]; ok {
			totalUsed.Add(used)
		}
	}
	if totalUsed.Cmp(totalCapacity) >= 0 {
		return "cr_exhausted"
	}

	for _, host := range inputHosts {
		if evaluator.HasUsableSlot(host, projectID, flavorGroupName, vmMemBytes) {
			return "slot_blocked"
		}
	}
	return "slot_exhausted"
}

// ClassifyPlacement determines the CR slot outcome for a successful placement:
//   - no_cr: no active CR or CR capacity fully exhausted (H1)
//   - slot_missed: CR has remaining capacity but no candidate host has a slot with remaining > 0 (H2)
//   - slot_used: CR has remaining capacity and at least one candidate host has a slot with remaining > 0 (H3)
func ClassifyPlacement(
	evaluator *SlotEvaluator,
	activeCRs []v1alpha1.CommittedResource,
	candidateHosts []string,
	projectID, flavorGroupName string,
) string {

	if len(activeCRs) == 0 {
		return "no_cr"
	}
	totalCapacity := resource.Quantity{}
	totalUsed := resource.Quantity{}
	for _, cr := range activeCRs {
		totalCapacity.Add(cr.Spec.Amount)
		if used, ok := cr.Status.UsedResources["memory"]; ok {
			totalUsed.Add(used)
		}
	}
	if totalUsed.Cmp(totalCapacity) >= 0 {
		return "no_cr"
	}
	for _, host := range candidateHosts {
		for _, slot := range evaluator.SlotsForHost(host, projectID, flavorGroupName) {
			if ReservationRemainingMemory(slot) > 0 {
				return "slot_used"
			}
		}
	}
	return "slot_missed"
}

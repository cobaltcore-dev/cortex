// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crs

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SlotEvaluator provides post-pipeline evaluation of CR reservation slots.
// Build once per request from HV and Reservation CRDs; query without further reads.
type SlotEvaluator struct {
	// hvFreeMemory is (EffectiveCapacity - Allocation).memory per host, before reservation blocks.
	hvFreeMemory map[string]int64
	// reservationsByHost maps host name to its ready CR reservation slots.
	reservationsByHost map[string][]v1alpha1.Reservation
}

// BuildSlotEvaluator lists HV CRDs and CR Reservation CRDs once and returns an evaluator
// that can answer slot-usability queries without further K8s reads.
func BuildSlotEvaluator(ctx context.Context, c client.Client) (*SlotEvaluator, error) {
	eval := &SlotEvaluator{
		hvFreeMemory:       make(map[string]int64),
		reservationsByHost: make(map[string][]v1alpha1.Reservation),
	}

	var hvList hv1.HypervisorList
	if err := c.List(ctx, &hvList); err != nil {
		return nil, err
	}
	for _, hv := range hvList.Items {
		var capacityMap map[hv1.ResourceName]resource.Quantity
		if hv.Status.EffectiveCapacity != nil {
			capacityMap = hv.Status.EffectiveCapacity
		} else {
			capacityMap = hv.Status.Capacity
		}
		effectiveMemQ := capacityMap[hv1.ResourceMemory]
		allocMemQ := hv.Status.Allocation[hv1.ResourceMemory]
		eval.hvFreeMemory[hv.Name] = max(effectiveMemQ.Value()-allocMemQ.Value(), 0)
	}

	var resList v1alpha1.ReservationList
	if err := c.List(ctx, &resList,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		return nil, err
	}
	for _, res := range resList.Items {
		if !res.IsReady() {
			continue
		}
		if res.Spec.CommittedResourceReservation == nil {
			continue
		}
		eval.reservationsByHost[res.Spec.TargetHost] = append(eval.reservationsByHost[res.Spec.TargetHost], res)
	}

	return eval, nil
}

// SlotsForHost returns all CR reservation slots on hostName matching projectID + flavorGroup.
func (e *SlotEvaluator) SlotsForHost(hostName, projectID, flavorGroup string) []v1alpha1.Reservation {
	var result []v1alpha1.Reservation
	for _, res := range e.reservationsByHost[hostName] {
		if res.Spec.CommittedResourceReservation.MatchesGroup(projectID, flavorGroup) {
			result = append(result, res)
		}
	}
	return result
}

// HasUsableSlot reports whether hostName has at least one CR slot that can accommodate
// a VM of vmMemBytes under the overfill model:
//
//	slot.remaining + host.base_free >= vmMemBytes
//
// where host.base_free = hvFreeMemory[host] - sum(all reservation blocks on host).
// On happy-path candidates the pipeline already guarantees host capacity, so this
// simplifies to slot.remaining > 0 — but the full formula is evaluated regardless.
func (e *SlotEvaluator) HasUsableSlot(hostName, projectID, flavorGroup string, vmMemBytes int64) bool {
	var allBlocks int64
	for _, res := range e.reservationsByHost[hostName] {
		blockQ := res.Spec.Resources[hv1.ResourceMemory]
		allBlocks += blockQ.Value()
	}
	hvFree := e.hvFreeMemory[hostName]

	for _, slot := range e.SlotsForHost(hostName, projectID, flavorGroup) {
		slotRemaining := ReservationRemainingMemory(slot)
		if slotRemaining <= 0 {
			continue
		}
		// host.base_free + slot.remaining = hvFree - allBlocks + slotRemaining
		if hvFree-allBlocks+slotRemaining >= vmMemBytes {
			return true
		}
	}
	return false
}

// ReservationRemainingMemory returns how many bytes of memory remain
// unallocated in a reservation slot. Returns 0 if the slot is full or nil.
func ReservationRemainingMemory(res v1alpha1.Reservation) int64 {
	cr := res.Spec.CommittedResourceReservation
	if cr == nil {
		return 0
	}
	totalMemQ := res.Spec.Resources[hv1.ResourceMemory]
	var usedMem int64
	for _, alloc := range cr.Allocations {
		allocMem := alloc.Resources[hv1.ResourceMemory]
		usedMem += allocMem.Value()
	}
	return max(totalMemQ.Value()-usedMem, 0)
}

// PickSlot selects the best reservation slot for a new VM.
// A slot is usable if it has any remaining memory (overfill is allowed: the VM
// may exceed the slot's remaining capacity, with the overflow covered by the
// host's free capacity which the pipeline already verified).
// Selection: maximise coverage (min(remMem, vmMemoryBytes)), tiebreak by
// smallest remaining memory (tightest fit), then reservation name.
// Returns the slot name, or "" if no slot has any remaining memory.
func PickSlot(candidates []v1alpha1.Reservation, vmMemoryBytes int64) string {
	bestName := ""
	var bestCoverage, bestRemMem int64

	for _, res := range candidates {
		remMem := ReservationRemainingMemory(res)
		if remMem <= 0 {
			continue
		}
		coverage := min(remMem, vmMemoryBytes)

		if bestName == "" ||
			coverage > bestCoverage ||
			(coverage == bestCoverage && remMem < bestRemMem) ||
			(coverage == bestCoverage && remMem == bestRemMem && res.Name < bestName) {
			bestName = res.Name
			bestCoverage = coverage
			bestRemMem = remMem
		}
	}

	return bestName
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CRSlotEvaluator provides post-pipeline evaluation of CR reservation slots.
// Build once per request from HV and Reservation CRDs; query without further reads.
type CRSlotEvaluator struct {
	// hvFreeMemory is (EffectiveCapacity - Allocation).memory per host, before reservation blocks.
	hvFreeMemory map[string]int64
	// reservationsByHost maps host name to its ready CR reservation slots.
	reservationsByHost map[string][]v1alpha1.Reservation
}

// BuildCRSlotEvaluator lists HV CRDs and CR Reservation CRDs once and returns an evaluator
// that can answer slot-usability queries without further K8s reads.
func BuildCRSlotEvaluator(ctx context.Context, c client.Client) (*CRSlotEvaluator, error) {
	eval := &CRSlotEvaluator{
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
		effectiveMem := effectiveMemQ.Value()
		allocMem := allocMemQ.Value()
		eval.hvFreeMemory[hv.Name] = max(effectiveMem-allocMem, 0)
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
		host := res.Spec.TargetHost
		eval.reservationsByHost[host] = append(eval.reservationsByHost[host], res)
	}

	return eval, nil
}

// SlotsForHost returns all CR reservation slots on hostName matching projectID + flavorGroup.
func (e *CRSlotEvaluator) SlotsForHost(hostName, projectID, flavorGroup string) []v1alpha1.Reservation {
	var result []v1alpha1.Reservation
	for _, res := range e.reservationsByHost[hostName] {
		cr := res.Spec.CommittedResourceReservation
		if cr.MatchesGroup(projectID, flavorGroup) {
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
func (e *CRSlotEvaluator) HasUsableSlot(hostName, projectID, flavorGroup string, vmMemBytes int64) bool {
	var allBlocks int64
	for _, res := range e.reservationsByHost[hostName] {
		blockQ := res.Spec.Resources[hv1.ResourceMemory]
		allBlocks += blockQ.Value()
	}
	hvFree := e.hvFreeMemory[hostName]

	for _, slot := range e.SlotsForHost(hostName, projectID, flavorGroup) {
		slotRemaining := reservationRemainingMemory(slot)
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

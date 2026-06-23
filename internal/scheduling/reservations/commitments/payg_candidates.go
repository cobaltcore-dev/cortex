// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"sort"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PAYGCandidate is an unallocated PAYG VM suitable for pre-allocation into a reservation slot.
type PAYGCandidate struct {
	VMID       string
	HVName     string
	MemoryMB   uint64
	FlavorName string // exact flavor of the VM — used directly as the slot ResourceName
}

// ScanAZForPaygCandidates returns unallocated PAYG VMs across all HVs in az, grouped by HV name.
// Makes exactly two calls regardless of HV or VM count:
//  1. List all CR Reservations → build allocated VM UUID set from Spec.Allocations
//  2. VMSource.ListVMsByProject → enriched VM data for the project
//
// HVs are identified by the label "topology.kubernetes.io/zone".
func ScanAZForPaygCandidates(
	ctx context.Context,
	k8sClient client.Client,
	vmSource reservations.VMSource,
	az string,
	projectID string,
	flavorGroup compute.FlavorGroupFeature,
) (map[string][]PAYGCandidate, error) {

	var hvList hv1.HypervisorList
	if err := k8sClient.List(ctx, &hvList); err != nil {
		return nil, err
	}
	azHVs := make(map[string]*hv1.Hypervisor)
	for i := range hvList.Items {
		hv := &hvList.Items[i]
		if hv.Labels["topology.kubernetes.io/zone"] == az {
			azHVs[hv.Name] = hv
		}
	}
	if len(azHVs) == 0 {
		return nil, nil
	}

	// One cache scan: build the set of VM UUIDs already claimed by any CR Reservation.
	// Spec.Allocations is the scheduling perspective — exactly what we need here.
	allocatedVMIDs, err := buildAllocatedVMSet(ctx, k8sClient, az)
	if err != nil {
		return nil, err
	}

	projectVMs, err := vmSource.ListVMsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	vmsByHV := make(map[string][]reservations.VM, len(azHVs))
	for _, vm := range projectVMs {
		if _, inAZ := azHVs[vm.CurrentHypervisor]; inAZ {
			vmsByHV[vm.CurrentHypervisor] = append(vmsByHV[vm.CurrentHypervisor], vm)
		}
	}

	result := make(map[string][]PAYGCandidate)
	for hvName, hv := range azHVs {
		candidates := filterPaygCandidates(hvName, hv, vmsByHV[hvName], flavorGroup, allocatedVMIDs)
		if len(candidates) > 0 {
			result[hvName] = candidates
		}
	}
	return result, nil
}

// buildAllocatedVMSet lists CR Reservations in az and returns the set of VM UUIDs present
// in any Spec.Allocations. Filtering by AZ keeps the set small — only slots relevant
// to the current scan are included. One cache scan shared across all HV filters.
func buildAllocatedVMSet(ctx context.Context, k8sClient client.Client, az string) (map[string]struct{}, error) {
	var resList v1alpha1.ReservationList
	if err := k8sClient.List(ctx, &resList,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		return nil, err
	}
	allocated := make(map[string]struct{})
	for _, res := range resList.Items {
		if res.Spec.AvailabilityZone != az {
			continue
		}
		if res.Spec.CommittedResourceReservation == nil {
			continue
		}
		for vmUUID := range res.Spec.CommittedResourceReservation.Allocations {
			allocated[vmUUID] = struct{}{}
		}
	}
	return allocated, nil
}

// filterPaygCandidates returns unallocated PAYG VMs from a pre-fetched list for one HV.
// vms must already be filtered to hvName by the caller.
// allocatedVMIDs is a pre-built set of UUIDs already claimed by any CR Reservation.Spec.Allocations.
// Used by reuse sites (ticket #410, #372) where the caller already holds VM data and the allocated set.
func filterPaygCandidates(
	hvName string,
	hv *hv1.Hypervisor,
	vms []reservations.VM,
	flavorGroup compute.FlavorGroupFeature,
	allocatedVMIDs map[string]struct{},
) []PAYGCandidate {

	if len(vms) == 0 {
		return nil
	}

	// Build set of active instance UUIDs from the HV CRD for physical-presence check.
	activeOnHV := make(map[string]bool, len(hv.Status.Instances))
	for _, inst := range hv.Status.Instances {
		if inst.Active {
			activeOnHV[inst.ID] = true
		}
	}

	// Build set of flavor names in the group for O(1) membership check.
	flavorNames := make(map[string]uint64, len(flavorGroup.Flavors))
	for _, f := range flavorGroup.Flavors {
		flavorNames[f.Name] = f.MemoryMB
	}

	var candidates []PAYGCandidate
	for _, vm := range vms {
		memMB, inGroup := flavorNames[vm.FlavorName]
		if !inGroup {
			continue
		}
		if !activeOnHV[vm.UUID] {
			continue
		}
		if _, isAllocated := allocatedVMIDs[vm.UUID]; isAllocated {
			continue
		}
		candidates = append(candidates, PAYGCandidate{
			VMID:       vm.UUID,
			HVName:     hvName,
			MemoryMB:   memMB,
			FlavorName: vm.FlavorName,
		})
	}

	sortCandidatesDesc(candidates)
	return candidates
}

// sortCandidatesDesc sorts candidates descending by memory, with UUID as a stable tie-break.
func sortCandidatesDesc(candidates []PAYGCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].MemoryMB != candidates[j].MemoryMB {
			return candidates[i].MemoryMB > candidates[j].MemoryMB
		}
		return candidates[i].VMID < candidates[j].VMID
	})
}

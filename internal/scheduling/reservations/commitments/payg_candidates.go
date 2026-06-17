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
// Makes exactly one VMSource call regardless of the number of HVs in the AZ.
// HVs are identified by the label "topology.kubernetes.io/zone".
func ScanAZForPaygCandidates(
	ctx context.Context,
	k8sClient client.Client,
	vmSource reservations.VMSource,
	az string,
	projectID string,
	flavorGroup compute.FlavorGroupFeature,
) (map[string][]PAYGCandidate, error) {
	// List all HVs from cache; filter to the target AZ.
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

	// One Postgres call for all VMs in the project.
	projectVMs, err := vmSource.ListVMsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	// Group project VMs by hypervisor for O(1) per-HV lookup.
	vmsByHV := make(map[string][]reservations.VM, len(azHVs))
	for _, vm := range projectVMs {
		if _, inAZ := azHVs[vm.CurrentHypervisor]; inAZ {
			vmsByHV[vm.CurrentHypervisor] = append(vmsByHV[vm.CurrentHypervisor], vm)
		}
	}

	result := make(map[string][]PAYGCandidate)
	for hvName, hv := range azHVs {
		candidates, err := filterPaygCandidates(ctx, k8sClient, hvName, hv, vmsByHV[hvName], flavorGroup)
		if err != nil {
			return nil, err
		}
		if len(candidates) > 0 {
			result[hvName] = candidates
		}
	}
	return result, nil
}

// filterPaygCandidates returns unallocated PAYG VMs from a pre-fetched list for one HV.
// vms must already be filtered to hvName (by the caller). Used by reuse sites (ticket #410,
// #372) where the caller already holds a pre-fetched VM list.
func filterPaygCandidates(
	ctx context.Context,
	k8sClient client.Client,
	hvName string,
	hv *hv1.Hypervisor,
	vms []reservations.VM,
	flavorGroup compute.FlavorGroupFeature,
) ([]PAYGCandidate, error) {

	if len(vms) == 0 {
		return nil, nil
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

		// Exclude VMs already claimed by any Reservation.Spec.Allocations.
		var allocated v1alpha1.ReservationList
		if err := k8sClient.List(ctx, &allocated,
			client.MatchingFields{idxReservationByAllocationVMUUID: vm.UUID},
		); err != nil {
			return nil, err
		}
		if len(allocated.Items) > 0 {
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
	return candidates, nil
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

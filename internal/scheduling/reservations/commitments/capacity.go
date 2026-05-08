// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// CapacityCalculator computes capacity reports for Limes LIQUID API.
type CapacityCalculator struct {
	client client.Client
}

func NewCapacityCalculator(client client.Client) *CapacityCalculator {
	return &CapacityCalculator{client: client}
}

// CalculateCapacity computes per-AZ capacity for all flavor groups.
// For each flavor group, three resources are reported: _ram, _cores, _instances.
// Capacity and usage are read from FlavorGroupCapacity CRDs pre-computed by the capacity controller.
// Usage is approximated from slot counts (total − placeable of the smallest flavor); this may
// slightly under-report usage when larger flavors are running, showing more free capacity than
// reality — acceptable for capacity planning purposes.
func (c *CapacityCalculator) CalculateCapacity(ctx context.Context, req liquid.ServiceCapacityRequest) (liquid.ServiceCapacityReport, error) {
	// Get all flavor groups from Knowledge CRDs (needed for smallest-flavor lookup).
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Get version from Knowledge CRD (same as info API version).
	var infoVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		infoVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	// List all FlavorGroupCapacity CRDs and index by (flavorGroup, az).
	var capacityList v1alpha1.FlavorGroupCapacityList
	if err := c.client.List(ctx, &capacityList); err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to list FlavorGroupCapacity CRDs: %w", err)
	}
	type groupAZKey struct{ group, az string }
	crdByKey := make(map[groupAZKey]*v1alpha1.FlavorGroupCapacity, len(capacityList.Items))
	for i := range capacityList.Items {
		crd := &capacityList.Items[i]
		crdByKey[groupAZKey{crd.Spec.FlavorGroup, crd.Spec.AvailabilityZone}] = crd
	}

	// Build capacity report for all flavor groups.
	report := liquid.ServiceCapacityReport{
		InfoVersion: infoVersion,
		Resources:   make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	logger := LoggerFromContext(ctx)
	for groupName, groupData := range flavorGroups {
		smallestFlavorName := groupData.SmallestFlavor.Name
		memoryMBPerSlot := groupData.SmallestFlavor.MemoryMB
		vcpusPerSlot := groupData.SmallestFlavor.VCPUs

		ramAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		coresAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		instancesAZCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))

		for _, az := range req.AllAZs {
			crd, ok := crdByKey[groupAZKey{groupName, string(az)}]
			if !ok {
				// No CRD for this (group, AZ) pair — report zero.
				zero := &liquid.AZResourceCapacityReport{Capacity: 0}
				ramAZCapacity[az] = zero
				coresAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				instancesAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			// If the CRD data is stale, report last-known capacity but omit usage.
			if !apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
				logger.Info("FlavorGroupCapacity CRD is stale, reporting capacity without usage",
					"flavorGroup", groupName, "az", az)
			}

			// Find the smallest-flavor entry in the CRD status.
			var smallest *v1alpha1.FlavorCapacityStatus
			for i := range crd.Status.Flavors {
				if crd.Status.Flavors[i].FlavorName == smallestFlavorName {
					smallest = &crd.Status.Flavors[i]
					break
				}
			}
			if smallest == nil {
				zero := &liquid.AZResourceCapacityReport{Capacity: 0}
				ramAZCapacity[az] = zero
				coresAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				instancesAZCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			totalSlots := uint64(smallest.TotalCapacityVMSlots) //nolint:gosec // slot count from CRD, realistically bounded
			ramEntry := &liquid.AZResourceCapacityReport{Capacity: totalSlots * memoryMBPerSlot / 1024}
			coresEntry := &liquid.AZResourceCapacityReport{Capacity: totalSlots * vcpusPerSlot}
			instancesEntry := &liquid.AZResourceCapacityReport{Capacity: totalSlots}

			// Usage is approximated from slot counts. This may slightly under-report usage when
			// larger flavors are running (safe direction: shows more free capacity than reality).
			if apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
				placeableSlots := uint64(smallest.PlaceableVMs) //nolint:gosec // slot count from CRD, realistically bounded
				var usedSlots uint64
				if totalSlots > placeableSlots {
					usedSlots = totalSlots - placeableSlots
				}
				ramEntry.Usage = Some[uint64](usedSlots * memoryMBPerSlot / 1024)
				coresEntry.Usage = Some[uint64](usedSlots * vcpusPerSlot)
				instancesEntry.Usage = Some[uint64](usedSlots)
			}
			ramAZCapacity[az] = ramEntry
			coresAZCapacity[az] = coresEntry
			instancesAZCapacity[az] = instancesEntry
		}

		report.Resources[liquid.ResourceName(ResourceNameRAM(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: ramAZCapacity,
		}
		report.Resources[liquid.ResourceName(ResourceNameCores(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: coresAZCapacity,
		}
		report.Resources[liquid.ResourceName(ResourceNameInstances(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: instancesAZCapacity,
		}
	}

	return report, nil
}

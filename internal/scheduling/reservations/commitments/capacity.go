// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/sapcc/go-api-declarations/liquid"
	. "go.xyrillian.de/gg/option"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// CapacityCalculator computes capacity reports for Limes LIQUID API.
type CapacityCalculator struct {
	client client.Client
	conf   APIConfig
}

func NewCapacityCalculator(client client.Client, conf APIConfig) *CapacityCalculator {
	return &CapacityCalculator{client: client, conf: conf}
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
		resCfg := c.conf.ResourceConfigForGroup(groupName)
		// Skip groups not configured for capacity reporting.
		if !resCfg.RAM.HasCapacity && !resCfg.Cores.HasCapacity && !resCfg.Instances.HasCapacity {
			continue
		}

		smallestFlavorName := groupData.SmallestFlavor.Name
		// Add 16 MiB before dividing: flavors reserve 16 MiB for video RAM (hw_video:ram_max_mb=16),
		// so a nominal "2 GiB" flavor has 2032 MiB.
		memoryMBPerSlot := groupData.SmallestFlavor.MemoryMB + 16
		vcpusPerSlot := groupData.SmallestFlavor.VCPUs

		// Fixed-ratio groups expose RAM in slot units (1 unit = 1 smallest-flavor slot).
		// Variable-ratio groups expose RAM in GiB.
		isFixedRatio := groupData.RamCoreRatio != nil

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
			var ramCapacity uint64
			if isFixedRatio {
				ramCapacity = totalSlots
			} else {
				ramCapacity = totalSlots * memoryMBPerSlot / 1024
			}
			ramEntry := &liquid.AZResourceCapacityReport{Capacity: ramCapacity}
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
				if isFixedRatio {
					ramEntry.Usage = Some[uint64](usedSlots)
				} else {
					ramEntry.Usage = Some[uint64](usedSlots * memoryMBPerSlot / 1024)
				}
				coresEntry.Usage = Some[uint64](usedSlots * vcpusPerSlot)
				instancesEntry.Usage = Some[uint64](usedSlots)
			}
			ramAZCapacity[az] = ramEntry
			coresAZCapacity[az] = coresEntry
			instancesAZCapacity[az] = instancesEntry
		}

		if resCfg.RAM.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameRAM(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: ramAZCapacity,
			}
		}
		if resCfg.Cores.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameCores(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: coresAZCapacity,
			}
		}
		if resCfg.Instances.HasCapacity {
			report.Resources[liquid.ResourceName(ResourceNameInstances(groupName))] = &liquid.ResourceCapacityReport{
				PerAZ: instancesAZCapacity,
			}
		}
	}

	return report, nil
}

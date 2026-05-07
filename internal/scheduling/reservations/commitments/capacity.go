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

		azCapacity := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(req.AllAZs))
		for _, az := range req.AllAZs {
			crd, ok := crdByKey[groupAZKey{groupName, string(az)}]
			if !ok {
				// No CRD for this (group, AZ) pair — report zero.
				azCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			// If the CRD data is stale, report last-known capacity but omit usage.
			ready := apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady)
			if !ready {
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
				azCapacity[az] = &liquid.AZResourceCapacityReport{Capacity: 0}
				continue
			}

			capacity := uint64(smallest.TotalCapacityVMSlots) //nolint:gosec
			azEntry := &liquid.AZResourceCapacityReport{Capacity: capacity}
			if ready {
				placeable := uint64(smallest.PlaceableVMs) //nolint:gosec
				var usage uint64
				if capacity > placeable {
					usage = capacity - placeable
				}
				azEntry.Usage = Some[uint64](usage)
			}
			azCapacity[az] = azEntry
		}

		// All three resources share the same capacity units (multiples of smallest flavor).
		report.Resources[liquid.ResourceName(ResourceNameRAM(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: azCapacity,
		}
		report.Resources[liquid.ResourceName(ResourceNameCores(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: c.copyAZCapacity(azCapacity),
		}
		report.Resources[liquid.ResourceName(ResourceNameInstances(groupName))] = &liquid.ResourceCapacityReport{
			PerAZ: c.copyAZCapacity(azCapacity),
		}
	}

	return report, nil
}

// copyAZCapacity creates a deep copy of the AZ capacity map.
// Each resource needs its own map instance.
func (c *CapacityCalculator) copyAZCapacity(
	src map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport,
) map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport {

	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, len(src))
	for az, report := range src {
		result[az] = &liquid.AZResourceCapacityReport{
			Capacity: report.Capacity,
			Usage:    report.Usage,
		}
	}
	return result
}

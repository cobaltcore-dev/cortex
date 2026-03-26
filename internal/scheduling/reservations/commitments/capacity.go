// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// All flavor groups are included, not just those with fixed RAM/core ratio.
func (c *CapacityCalculator) CalculateCapacity(ctx context.Context) (liquid.ServiceCapacityReport, error) {
	// Get all flavor groups from Knowledge CRDs
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Get version from Knowledge CRD (same as info API version)
	var infoVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		infoVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	// Build capacity report for all flavor groups
	report := liquid.ServiceCapacityReport{
		InfoVersion: infoVersion,
		Resources:   make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	for groupName, groupData := range flavorGroups {
		// All flavor groups are included in capacity reporting (not just those with fixed ratio).

		// Calculate per-AZ capacity (placeholder: capacity=0 for all resources)
		azCapacity, err := c.calculateAZCapacity(ctx, groupName, groupData)
		if err != nil {
			return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to calculate capacity for %s: %w", groupName, err)
		}

		// === 1. RAM Resource ===
		ramResourceName := liquid.ResourceName(ResourceNameRAM(groupName))
		report.Resources[ramResourceName] = &liquid.ResourceCapacityReport{
			PerAZ: azCapacity,
		}

		// === 2. Cores Resource ===
		// NOTE: Copying RAM capacity is only valid while capacity=0 (placeholder).
		// When real capacity is implemented, derive cores capacity with unit conversion
		// (e.g., cores = RAM / ramCoreRatio). See calculateAZCapacity for details.
		coresResourceName := liquid.ResourceName(ResourceNameCores(groupName))
		report.Resources[coresResourceName] = &liquid.ResourceCapacityReport{
			PerAZ: c.copyAZCapacity(azCapacity),
		}

		// === 3. Instances Resource ===
		// NOTE: Same as cores - copying is only valid while capacity=0 (placeholder).
		// When real capacity is implemented, derive instances capacity appropriately.
		instancesResourceName := liquid.ResourceName(ResourceNameInstances(groupName))
		report.Resources[instancesResourceName] = &liquid.ResourceCapacityReport{
			PerAZ: c.copyAZCapacity(azCapacity),
		}
	}

	return report, nil
}

// copyAZCapacity creates a deep copy of the AZ capacity map.
// This is needed because each resource needs its own map instance.
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

func (c *CapacityCalculator) calculateAZCapacity(
	ctx context.Context,
	_ string, // groupName - reserved for future use
	_ compute.FlavorGroupFeature, // groupData - reserved for future use
) (map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, error) {
	// Get list of availability zones from HostDetails Knowledge
	azs, err := c.getAvailabilityZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get availability zones: %w", err)
	}

	// Create report entry for each AZ with placeholder capacity=0.
	//
	// NOTE: When implementing real capacity calculation here, you MUST also update
	// the copying logic in CalculateCapacity() for _cores and _instances resources.
	// Those resources use different units (vCPUs and VM count) than _ram (memory multiples),
	// so the capacity values cannot be simply copied - they require unit conversion:
	//   - _cores capacity = RAM capacity / ramCoreRatio
	//   - _instances capacity = needs its own derivation logic
	//
	// TODO: Calculate actual capacity from Reservation CRDs or host resources
	// TODO: Calculate actual usage from VM allocations
	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport)
	for _, az := range azs {
		result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
			Capacity: 0,               // Placeholder: capacity=0 until actual calculation is implemented
			Usage:    Some[uint64](0), // Placeholder: usage=0 until actual calculation is implemented
		}
	}

	return result, nil
}

func (c *CapacityCalculator) getAvailabilityZones(ctx context.Context) ([]string, error) {
	// List all Knowledge CRDs to find host-details knowledge
	var knowledgeList v1alpha1.KnowledgeList
	if err := c.client.List(ctx, &knowledgeList); err != nil {
		return nil, fmt.Errorf("failed to list Knowledge CRDs: %w", err)
	}

	// Find host-details knowledge and extract AZs
	azSet := make(map[string]struct{})
	for _, knowledge := range knowledgeList.Items {
		// Look for host-details extractor
		if knowledge.Spec.Extractor.Name != "host_details" {
			continue
		}

		// Parse features from Raw data
		features, err := v1alpha1.UnboxFeatureList[compute.HostDetails](knowledge.Status.Raw)
		if err != nil {
			// Skip if we can't parse this knowledge
			continue
		}

		// Collect unique AZ names
		for _, feature := range features {
			if feature.AvailabilityZone != "" {
				azSet[feature.AvailabilityZone] = struct{}{}
			}
		}
	}

	// Convert set to sorted slice
	azs := make([]string, 0, len(azSet))
	for az := range azSet {
		azs = append(azs, az)
	}
	sort.Strings(azs)

	return azs, nil
}

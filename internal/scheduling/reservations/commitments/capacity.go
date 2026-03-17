// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"

	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
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
func (c *CapacityCalculator) CalculateCapacity(ctx context.Context) (liquid.ServiceCapacityReport, error) {
	// Get all flavor groups from Knowledge CRDs
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Build capacity report per flavor group
	report := liquid.ServiceCapacityReport{
		Resources: make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	for groupName, groupData := range flavorGroups {
		// Resource name follows pattern: ram_<flavorgroup>
		resourceName := liquid.ResourceName("ram_" + groupName)

		// Calculate per-AZ capacity and usage
		azCapacity, err := c.calculateAZCapacity(ctx, groupName, groupData)
		if err != nil {
			return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to calculate capacity for %s: %w", groupName, err)
		}

		report.Resources[resourceName] = &liquid.ResourceCapacityReport{
			PerAZ: azCapacity,
		}
	}

	return report, nil
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

	// Create report entry for each AZ with empty capacity/usage
	// Capacity and Usage are left unset (zero value of option.Option[uint64])
	// This signals to Limes: "These AZs exist, but capacity/usage not yet calculated"
	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport)
	for _, az := range azs {
		result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
			// Both Capacity and Usage left unset (empty optional values)
			// TODO: Calculate actual capacity from Reservation CRDs or host resources
			// TODO: Calculate actual usage from VM allocations
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

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"
	"time"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// CapacityCalculator computes capacity reports for Limes LIQUID API.
type CapacityCalculator struct {
	client          client.Client
	schedulerClient *reservations.SchedulerClient
}

func NewCapacityCalculator(client client.Client) *CapacityCalculator {
	schedulerClient := reservations.NewSchedulerClient("http://localhost:8080/scheduler/nova/external")
	return &CapacityCalculator{
		client:          client,
		schedulerClient: schedulerClient,
	}
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
	groupName string,
	groupData compute.FlavorGroupFeature,
) (map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, error) {

	azs, err := c.getAvailabilityZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get availability zones: %w", err)
	}

	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport)
	for _, az := range azs {
		capacity, usage, err := c.calculateInstanceCapacity(ctx, groupName, groupData, az)
		if err != nil {
			// Log error but continue with empty values for this AZ
			result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{}
			continue
		}

		result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
			Capacity: capacity,
			Usage:    Some(usage),
		}
	}

	return result, nil
}

// getHostAZMap returns a map from compute host name to availability zone.
func (c *CapacityCalculator) getHostAZMap(ctx context.Context) (map[string]string, error) {
	var knowledgeList v1alpha1.KnowledgeList
	if err := c.client.List(ctx, &knowledgeList); err != nil {
		return nil, fmt.Errorf("failed to list Knowledge CRDs: %w", err)
	}

	hostAZMap := make(map[string]string)
	for _, knowledge := range knowledgeList.Items {
		if knowledge.Spec.Extractor.Name != "host_details" {
			continue
		}
		features, err := v1alpha1.UnboxFeatureList[compute.HostDetails](knowledge.Status.Raw)
		if err != nil {
			continue
		}
		for _, feature := range features {
			if feature.ComputeHost != "" && feature.AvailabilityZone != "" {
				hostAZMap[feature.ComputeHost] = feature.AvailabilityZone
			}
		}
	}

	return hostAZMap, nil
}

func (c *CapacityCalculator) getAvailabilityZones(ctx context.Context) ([]string, error) {
	hostAZMap, err := c.getHostAZMap(ctx)
	if err != nil {
		return nil, err
	}

	azSet := make(map[string]struct{})
	for _, az := range hostAZMap {
		azSet[az] = struct{}{}
	}

	azs := make([]string, 0, len(azSet))
	for az := range azSet {
		azs = append(azs, az)
	}
	sort.Strings(azs)

	return azs, nil
}

// calculateInstanceCapacity returns the total capacity and current usage for a flavor group in an AZ.
// Capacity is expressed in multiples of the smallest flavor's memory.
// Total capacity is derived directly from Hypervisor CRDs (as if everything were empty).
// Currently available is derived from the scheduler (respecting current VM and reservation state).
// Usage = totalCapacity - currentlyAvailable.
func (c *CapacityCalculator) calculateInstanceCapacity(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	az string,
) (capacity, usage uint64, err error) {

	smallestFlavor := groupData.SmallestFlavor

	// Request 1: currently available — how many instances can be placed right now.
	currentResp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     fmt.Sprintf("capacity-current-%s-%s-%d", groupName, az, time.Now().UnixNano()),
		ProjectID:        "cortex-capacity-check",
		FlavorName:       smallestFlavor.Name,
		MemoryMB:         smallestFlavor.MemoryMB,
		VCPUs:            smallestFlavor.VCPUs,
		FlavorExtraSpecs: map[string]string{"hw_version": groupName},
		AvailabilityZone: az,
		Pipeline:         "kvm-general-purpose-load-balancing-all-filters-enabled",
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get current available capacity: %w", err)
	}
	currentlyAvailable := uint64(len(currentResp.Hosts))

	// Request 2: total capacity — hosts eligible if everything were empty.
	// Uses a dedicated pipeline that ignores VM allocations and all reservations.
	totalResp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     fmt.Sprintf("capacity-total-%s-%s-%d", groupName, az, time.Now().UnixNano()),
		ProjectID:        "cortex-capacity-check",
		FlavorName:       smallestFlavor.Name,
		MemoryMB:         smallestFlavor.MemoryMB,
		VCPUs:            smallestFlavor.VCPUs,
		FlavorExtraSpecs: map[string]string{"hw_version": groupName},
		AvailabilityZone: az,
		Pipeline:         "kvm-report-capacity",
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get total capacity: %w", err)
	}
	totalCapacity := uint64(len(totalResp.Hosts))

	var usageValue uint64
	if totalCapacity >= currentlyAvailable {
		usageValue = totalCapacity - currentlyAvailable
	}

	return totalCapacity, usageValue, nil
}

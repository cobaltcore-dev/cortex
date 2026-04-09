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
	currentPipeline string
	totalPipeline   string
}

func NewCapacityCalculator(client client.Client, config Config) *CapacityCalculator {
	return &CapacityCalculator{
		client:          client,
		schedulerClient: reservations.NewSchedulerClient(config.SchedulerURL),
		currentPipeline: config.ReportCapacityCurrentPipeline,
		totalPipeline:   config.ReportCapacityTotalPipeline,
	}
}

// CalculateCapacity computes per-AZ capacity for all flavor groups.
// For each flavor group, three resources are reported: _ram, _cores, _instances.
// All flavor groups are included, not just those with fixed RAM/core ratio.
// AZs are derived from HostDetails Knowledge CRDs.
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

	// Get availability zones from host details
	azs, err := c.getAvailabilityZones(ctx)
	if err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to get availability zones: %w", err)
	}

	// Build capacity report for all flavor groups
	report := liquid.ServiceCapacityReport{
		InfoVersion: infoVersion,
		Resources:   make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	for groupName, groupData := range flavorGroups {
		// Calculate per-AZ capacity using scheduler
		azCapacity, err := c.calculateAZCapacity(ctx, groupName, groupData, azs)
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

// calculateAZCapacity computes capacity per AZ for a flavor group via scheduler calls.
// On scheduler failure for an AZ, that AZ still gets an entry with capacity=0.
func (c *CapacityCalculator) calculateAZCapacity(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	azs []string,
) (map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport, error) {

	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport)
	for _, az := range azs {
		capacity, usage, err := c.calculateInstanceCapacity(ctx, groupName, groupData, az)
		if err != nil {
			// On failure, report az with capacity=0 rather than aborting entirely.
			result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
				Capacity: 0,
				Usage:    Some[uint64](0),
			}
			continue
		}
		result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
			Capacity: capacity,
			Usage:    Some[uint64](usage),
		}
	}
	return result, nil
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
		Pipeline:         c.currentPipeline,
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
		Pipeline:         c.totalPipeline,
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

// getHostAZMap returns a map from compute host name to availability zone.
func (c *CapacityCalculator) getHostAZMap(ctx context.Context) (map[string]string, error) {
	var knowledgeList v1alpha1.KnowledgeList
	if err := c.client.List(ctx, &knowledgeList); err != nil {
		return nil, fmt.Errorf("failed to list Knowledge CRDs: %w", err)
	}

	type hostAZEntry struct {
		ComputeHost      string `json:"ComputeHost"`
		AvailabilityZone string `json:"AvailabilityZone"`
	}

	hostAZMap := make(map[string]string)
	for _, knowledge := range knowledgeList.Items {
		if knowledge.Spec.Extractor.Name != "sap_host_details_extractor" {
			continue
		}
		features, err := v1alpha1.UnboxFeatureList[hostAZEntry](knowledge.Status.Raw)
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

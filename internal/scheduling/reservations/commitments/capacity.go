// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"time"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// For each flavor group, three resources are reported: _ram, _cores, _instances.
// All flavor groups are included, not just those with fixed RAM/core ratio.
// The request provides the list of all AZs from Limes that must be included in the report.
func (c *CapacityCalculator) CalculateCapacity(ctx context.Context, req liquid.ServiceCapacityRequest) (liquid.ServiceCapacityReport, error) {
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
		azCapacity := c.calculateAZCapacity(groupName, groupData, req.AllAZs)

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
	_ string, // groupName - reserved for future use
	_ compute.FlavorGroupFeature, // groupData - reserved for future use
	allAZs []liquid.AvailabilityZone, // list of all AZs from Limes request
) map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport {

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
	for _, az := range allAZs {
		result[az] = &liquid.AZResourceCapacityReport{
			Capacity: 0,               // Placeholder: capacity=0 until actual calculation is implemented
			Usage:    Some[uint64](0), // Placeholder: usage=0 until actual calculation is implemented
		}
	}

	return result
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

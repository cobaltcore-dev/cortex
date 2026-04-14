// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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
	totalPipeline   string
}

func NewCapacityCalculator(client client.Client, config Config) *CapacityCalculator {
	return &CapacityCalculator{
		client:          client,
		schedulerClient: reservations.NewSchedulerClient(config.SchedulerURL),
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

	// Pre-fetch all Hypervisor CRDs once (shared across all flavor groups and AZs)
	var hvList hv1.HypervisorList
	if err := c.client.List(ctx, &hvList); err != nil {
		return liquid.ServiceCapacityReport{}, fmt.Errorf("failed to list hypervisors: %w", err)
	}
	hvByName := make(map[string]hv1.Hypervisor, len(hvList.Items))
	for _, hv := range hvList.Items {
		hvByName[hv.Name] = hv
	}

	// Build capacity report for all flavor groups
	report := liquid.ServiceCapacityReport{
		InfoVersion: infoVersion,
		Resources:   make(map[liquid.ResourceName]*liquid.ResourceCapacityReport),
	}

	for groupName, groupData := range flavorGroups {
		// Calculate per-AZ capacity using scheduler + HV CRDs
		azCapacity := c.calculateAZCapacity(ctx, groupName, groupData, azs, hvByName)

		// === 1. RAM Resource ===
		ramResourceName := liquid.ResourceName(ResourceNameRAM(groupName))
		report.Resources[ramResourceName] = &liquid.ResourceCapacityReport{
			PerAZ: azCapacity,
		}

		// === 2. Cores Resource ===
		// All three resources express capacity in units of "multiples of the smallest flavor",
		// so the same number applies to ram, cores, and instances.
		coresResourceName := liquid.ResourceName(ResourceNameCores(groupName))
		report.Resources[coresResourceName] = &liquid.ResourceCapacityReport{
			PerAZ: c.copyAZCapacity(azCapacity),
		}

		// === 3. Instances Resource ===
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

// calculateAZCapacity computes capacity per AZ for a flavor group.
// Uses one scheduler call per AZ to get eligible hosts, then reads HV CRDs for resource data.
// On scheduler failure for an AZ, that AZ still gets an entry with capacity=0.
func (c *CapacityCalculator) calculateAZCapacity(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	azs []string,
	hvByName map[string]hv1.Hypervisor,
) map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport {

	result := make(map[liquid.AvailabilityZone]*liquid.AZResourceCapacityReport)
	for _, az := range azs {
		capacity, err := c.calculateInstanceCapacity(ctx, groupName, groupData, az, hvByName)
		if err != nil {
			// On failure, report az with capacity=0 rather than aborting entirely.
			result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
				Capacity: 0,
				Usage:    Some[uint64](0), // Placeholder: usage=0 until actual calculation is implemented
			}
			continue
		}
		result[liquid.AvailabilityZone(az)] = &liquid.AZResourceCapacityReport{
			Capacity: capacity,
			Usage:    Some[uint64](0), // Placeholder: usage=0 until actual calculation is implemented
		}
	}
	return result
}

// calculateInstanceCapacity returns the total capacity for a flavor group in an AZ.
// Capacity is expressed in multiples of the smallest flavor's memory.
// Usage tracking (VM allocations + reservations) is not yet implemented — see PR 2.
//
// 1. One scheduler call (kvm-report-capacity pipeline, ignoring allocations) → list of eligible hosts
// 2. For each eligible host, read EffectiveCapacity from HV CRDs
// 3. Total capacity = sum(floor(EffectiveCapacity.Memory / smallestFlavorMemory))
func (c *CapacityCalculator) calculateInstanceCapacity(
	ctx context.Context,
	groupName string,
	groupData compute.FlavorGroupFeature,
	az string,
	hvByName map[string]hv1.Hypervisor,
) (capacity uint64, err error) {

	smallestFlavor := groupData.SmallestFlavor
	smallestFlavorBytes := int64(smallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory from Nova, realistically bounded
	if smallestFlavorBytes <= 0 {
		return 0, fmt.Errorf("smallest flavor %q has invalid memory %d MB", smallestFlavor.Name, smallestFlavor.MemoryMB)
	}

	// Scheduler call: get eligible hosts (ignoring allocations and reservations).
	resp, err := c.schedulerClient.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     fmt.Sprintf("capacity-%s-%s-%d", groupName, az, time.Now().UnixNano()),
		ProjectID:        "cortex-capacity-check",
		FlavorName:       smallestFlavor.Name,
		MemoryMB:         smallestFlavor.MemoryMB,
		VCPUs:            smallestFlavor.VCPUs,
		FlavorExtraSpecs: map[string]string{"hw_version": groupName},
		AvailabilityZone: az,
		Pipeline:         c.totalPipeline,
	})
	if err != nil {
		return 0, fmt.Errorf("scheduler call failed: %w", err)
	}

	// For each eligible host, look up HV CRD and compute multiples.
	var totalCapacity uint64
	for _, hostName := range resp.Hosts {
		hv, ok := hvByName[hostName]
		if !ok {
			continue
		}

		// Use EffectiveCapacity if available, fall back to Capacity.
		effectiveCap := hv.Status.EffectiveCapacity
		if effectiveCap == nil {
			effectiveCap = hv.Status.Capacity
		}
		if effectiveCap == nil {
			continue
		}

		memCapacity, ok := effectiveCap[hv1.ResourceMemory]
		if !ok {
			continue
		}

		// Total: floor(effectiveCapacity / smallestFlavorMemory)
		capBytes := memCapacity.Value()
		if capBytes > 0 {
			totalCapacity += uint64(capBytes / smallestFlavorBytes) //nolint:gosec // both values are positive, result fits uint64
		}
	}

	return totalCapacity, nil
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

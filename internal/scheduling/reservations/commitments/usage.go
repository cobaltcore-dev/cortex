// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VMUsageInfo contains VM information needed for usage calculation.
// This is a local view of the VM enriched with flavor group information.
type VMUsageInfo struct {
	UUID          string
	Name          string
	FlavorName    string
	FlavorGroup   string
	Status        string
	MemoryMB      uint64
	VCPUs         uint64
	DiskGB        uint64
	VideoRAMMiB   *uint64 // optional, from flavor extra_specs hw_video:ram_max_mb
	AZ            string
	Hypervisor    string
	CreatedAt     time.Time
	UsageMultiple uint64 // Memory in multiples of smallest flavor in the group
}

// UsageCalculator computes usage reports for Limes LIQUID API.
type UsageCalculator struct {
	client  client.Client
	usageDB UsageDBClient
}

// NewUsageCalculator creates a new UsageCalculator instance.
func NewUsageCalculator(client client.Client, usageDB UsageDBClient) *UsageCalculator {
	return &UsageCalculator{
		client:  client,
		usageDB: usageDB,
	}
}

// CalculateUsage computes the usage report for a specific project.
func (c *UsageCalculator) CalculateUsage(
	ctx context.Context,
	log logr.Logger,
	projectID string,
	allAZs []liquid.AvailabilityZone,
) (liquid.ServiceUsageReport, error) {
	// Step 1: Get flavor groups from knowledge
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	// Get info version from Knowledge CRD (used by Limes to detect metadata changes)
	var infoVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		infoVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	// Step 2: Build commitment capacity map from K8s Reservation CRDs
	commitmentsByAZFlavorGroup, err := c.buildCommitmentCapacityMap(ctx, log, projectID)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to build commitment capacity map: %w", err)
	}

	// Step 3: Get and sort VMs for the project
	vms, err := c.getProjectVMs(ctx, log, projectID, flavorGroups, allAZs)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to get project VMs: %w", err)
	}
	sortVMsForUsageCalculation(vms)

	// Step 4: Assign VMs to commitments
	vmAssignments, assignedToCommitments := c.assignVMsToCommitments(vms, commitmentsByAZFlavorGroup)

	// Step 5: Build the response
	report := c.buildUsageResponse(vms, vmAssignments, flavorGroups, allAZs, infoVersion)

	log.Info("completed usage report",
		"projectID", projectID,
		"vmCount", len(vms),
		"assignedToCommitments", assignedToCommitments,
		"payg", len(vms)-assignedToCommitments,
		"commitments", countCommitmentStates(commitmentsByAZFlavorGroup),
		"resources", len(report.Resources))

	return report, nil
}

// azFlavorGroupKey creates a deterministic key for az:flavorGroup lookups.
func azFlavorGroupKey(az, flavorGroup string) string {
	return az + ":" + flavorGroup
}

// buildCommitmentCapacityMap retrieves all CR reservations for a project and builds
// a map of az:flavorGroup -> list of CommitmentStateWithUsage, sorted for deterministic assignment.
func (c *UsageCalculator) buildCommitmentCapacityMap(
	ctx context.Context,
	log logr.Logger,
	projectID string,
) (map[string][]*CommitmentStateWithUsage, error) {
	// List all committed resource reservations
	var allReservations v1alpha1.ReservationList
	if err := c.client.List(ctx, &allReservations, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		return nil, fmt.Errorf("failed to list reservations: %w", err)
	}

	// Group reservations by commitment UUID, filtering by project
	reservationsByCommitment := make(map[string][]v1alpha1.Reservation)
	for _, res := range allReservations.Items {
		if res.Spec.CommittedResourceReservation == nil {
			continue
		}
		if res.Spec.CommittedResourceReservation.ProjectID != projectID {
			continue
		}
		commitmentUUID := res.Spec.CommittedResourceReservation.CommitmentUUID
		reservationsByCommitment[commitmentUUID] = append(reservationsByCommitment[commitmentUUID], res)
	}

	// Build CommitmentState for each commitment and group by az:flavorGroup
	// Only include commitments that are currently active (started and not expired)
	now := time.Now()
	result := make(map[string][]*CommitmentStateWithUsage)
	for _, reservations := range reservationsByCommitment {
		state, err := FromReservations(reservations)
		if err != nil {
			log.Error(err, "failed to build commitment state from reservations")
			continue
		}

		// Skip commitments that haven't started yet
		if state.StartTime != nil && state.StartTime.After(now) {
			log.V(1).Info("skipping commitment that hasn't started yet",
				"commitmentUUID", state.CommitmentUUID,
				"startTime", state.StartTime)
			continue
		}

		// Skip commitments that have already expired
		if state.EndTime != nil && state.EndTime.Before(now) {
			log.V(1).Info("skipping expired commitment",
				"commitmentUUID", state.CommitmentUUID,
				"endTime", state.EndTime)
			continue
		}

		stateWithUsage := NewCommitmentStateWithUsage(state)
		key := azFlavorGroupKey(state.AvailabilityZone, state.FlavorGroupName)
		result[key] = append(result[key], stateWithUsage)
	}

	// Sort commitments within each az:flavorGroup for deterministic assignment
	for key := range result {
		sortCommitmentsForAssignment(result[key])
	}

	return result, nil
}

// getProjectVMs retrieves all VMs for a project from Postgres and enriches them with flavor group info.
func (c *UsageCalculator) getProjectVMs(
	ctx context.Context,
	log logr.Logger,
	projectID string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
) ([]VMUsageInfo, error) {

	if c.usageDB == nil {
		log.Info("usage DB client not configured - returning empty VM list", "projectID", projectID)
		return []VMUsageInfo{}, nil
	}

	rows, err := c.usageDB.ListProjectVMs(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs from Postgres: %w", err)
	}

	// Build flavor name -> flavor group lookup
	flavorToGroup := make(map[string]string)
	flavorToSmallestMemory := make(map[string]uint64) // for calculating usage multiples
	for groupName, group := range flavorGroups {
		for _, flavor := range group.Flavors {
			flavorToGroup[flavor.Name] = groupName
		}
		// Smallest flavor in group determines the usage unit
		if group.SmallestFlavor.Name != "" {
			for _, flavor := range group.Flavors {
				flavorToSmallestMemory[flavor.Name] = group.SmallestFlavor.MemoryMB
			}
		}
	}

	var vms []VMUsageInfo
	for _, row := range rows {
		// Parse creation time (Nova returns ISO 8601/RFC3339 format)
		createdAt, err := time.Parse(time.RFC3339, row.Created)
		if err != nil {
			log.V(1).Info("failed to parse server creation time, using zero time",
				"server", row.ID, "created", row.Created, "error", err.Error())
			createdAt = time.Time{}
		}

		// Determine flavor group
		flavorGroup := flavorToGroup[row.FlavorName]

		// Calculate usage multiple (memory in units of smallest flavor)
		var usageMultiple uint64
		if smallestMem := flavorToSmallestMemory[row.FlavorName]; smallestMem > 0 {
			usageMultiple = row.FlavorRAM / smallestMem
		}

		// Normalize AZ
		normalizedAZ := liquid.NormalizeAZ(row.AZ, allAZs)

		// Parse video RAM from flavor extra_specs
		var videoRAMMiB *uint64
		if row.FlavorExtras != "" {
			var extraSpecs map[string]string
			if err := json.Unmarshal([]byte(row.FlavorExtras), &extraSpecs); err == nil {
				if val, ok := extraSpecs["hw_video:ram_max_mb"]; ok {
					if parsed, err := strconv.ParseUint(val, 10, 64); err == nil {
						videoRAMMiB = &parsed
					}
				}
			}
		}

		vms = append(vms, VMUsageInfo{
			UUID:          row.ID,
			Name:          row.Name,
			FlavorName:    row.FlavorName,
			FlavorGroup:   flavorGroup,
			Status:        row.Status,
			MemoryMB:      row.FlavorRAM,
			VCPUs:         row.FlavorVCPUs,
			DiskGB:        row.FlavorDisk,
			VideoRAMMiB:   videoRAMMiB,
			AZ:            string(normalizedAZ),
			Hypervisor:    row.Hypervisor,
			CreatedAt:     createdAt,
			UsageMultiple: usageMultiple,
		})
	}

	return vms, nil
}

// sortVMsForUsageCalculation sorts VMs deterministically for usage calculation:
// 1. Oldest first (by CreatedAt)
// 2. Largest first (by MemoryMB)
// 3. Tie-break by UUID
func sortVMsForUsageCalculation(vms []VMUsageInfo) {
	sort.Slice(vms, func(i, j int) bool {
		// 1. Oldest first
		if !vms[i].CreatedAt.Equal(vms[j].CreatedAt) {
			return vms[i].CreatedAt.Before(vms[j].CreatedAt)
		}
		// 2. Largest first
		if vms[i].MemoryMB != vms[j].MemoryMB {
			return vms[i].MemoryMB > vms[j].MemoryMB
		}
		// 3. Tie-break by UUID
		return vms[i].UUID < vms[j].UUID
	})
}

// sortCommitmentsForAssignment sorts commitments deterministically:
// 1. Oldest first (by StartTime)
// 2. Largest capacity first (by TotalMemoryBytes)
// 3. Tie-break by CommitmentUUID
func sortCommitmentsForAssignment(commitments []*CommitmentStateWithUsage) {
	sort.Slice(commitments, func(i, j int) bool {
		// 1. Oldest first (nil StartTime treated as very old)
		iStart := commitments[i].StartTime
		jStart := commitments[j].StartTime
		iHasStart := iStart != nil
		jHasStart := jStart != nil
		switch {
		case iHasStart && jHasStart:
			if !iStart.Equal(*jStart) {
				return iStart.Before(*jStart)
			}
		case iHasStart:
			return false // j has nil, so j is "older"
		case jHasStart:
			return true // i has nil, so i is "older"
		}
		// 2. Largest capacity first
		if commitments[i].TotalMemoryBytes != commitments[j].TotalMemoryBytes {
			return commitments[i].TotalMemoryBytes > commitments[j].TotalMemoryBytes
		}
		// 3. Tie-break by UUID
		return commitments[i].CommitmentUUID < commitments[j].CommitmentUUID
	})
}

// assignVMsToCommitments assigns VMs to commitments based on az:flavorGroup matching.
// Returns a map of vmUUID -> commitmentUUID (empty string for PAYG VMs) and count of assigned VMs.
func (c *UsageCalculator) assignVMsToCommitments(
	vms []VMUsageInfo,
	commitmentsByAZFlavorGroup map[string][]*CommitmentStateWithUsage,
) (vmAssignments map[string]string, assignedCount int) {

	vmAssignments = make(map[string]string, len(vms))

	for _, vm := range vms {
		key := azFlavorGroupKey(vm.AZ, vm.FlavorGroup)
		commitments := commitmentsByAZFlavorGroup[key]

		vmMemoryBytes := int64(vm.MemoryMB) * 1024 * 1024 //nolint:gosec // VM memory from Nova, realistically bounded
		assigned := false

		// Try to assign to first commitment with remaining capacity
		for _, commitment := range commitments {
			if commitment.AssignVM(vm.UUID, vmMemoryBytes) {
				vmAssignments[vm.UUID] = commitment.CommitmentUUID
				assigned = true
				assignedCount++
				break
			}
		}

		if !assigned {
			// PAYG - no commitment assignment
			vmAssignments[vm.UUID] = ""
		}
	}

	return vmAssignments, assignedCount
}

// azUsageData aggregates usage data for a specific flavor group and AZ.
type azUsageData struct {
	ramUsage      uint64               // RAM usage in multiples of smallest flavor
	coresUsage    uint64               // Total vCPU count
	instanceCount uint64               // Number of VMs
	subresources  []liquid.Subresource // VM details for subresource reporting
}

// buildUsageResponse constructs the Liquid API ServiceUsageReport.
// All flavor groups are included in the report; commitment assignment only applies
// to groups with fixed RAM/core ratio (those that accept commitments).
// For each flavor group, three resources are reported: _ram, _cores, _instances.
func (c *UsageCalculator) buildUsageResponse(
	vms []VMUsageInfo,
	vmAssignments map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
	infoVersion int64,
) liquid.ServiceUsageReport {
	// Initialize resources map for all flavor groups
	resources := make(map[liquid.ResourceName]*liquid.ResourceUsageReport)

	// Group VMs by flavor group and AZ for aggregation
	usageByFlavorGroupAZ := make(map[string]map[liquid.AvailabilityZone]*azUsageData)

	for _, vm := range vms {
		if vm.FlavorGroup == "" {
			continue // Skip VMs without flavor group
		}

		// Initialize maps if needed
		if usageByFlavorGroupAZ[vm.FlavorGroup] == nil {
			usageByFlavorGroupAZ[vm.FlavorGroup] = make(map[liquid.AvailabilityZone]*azUsageData)
		}
		az := liquid.AvailabilityZone(vm.AZ)
		if usageByFlavorGroupAZ[vm.FlavorGroup][az] == nil {
			usageByFlavorGroupAZ[vm.FlavorGroup][az] = &azUsageData{}
		}

		// Accumulate usage for all resource types
		usageByFlavorGroupAZ[vm.FlavorGroup][az].ramUsage += vm.UsageMultiple
		usageByFlavorGroupAZ[vm.FlavorGroup][az].coresUsage += vm.VCPUs
		usageByFlavorGroupAZ[vm.FlavorGroup][az].instanceCount++

		// Build subresource attributes
		commitmentID := vmAssignments[vm.UUID]
		attributes := buildVMAttributes(vm, commitmentID)

		subresource, err := liquid.SubresourceBuilder[map[string]any]{
			ID:         vm.UUID,
			Attributes: attributes,
		}.Finalize()
		if err != nil {
			// This should never happen with valid attributes, skip this VM
			continue
		}

		usageByFlavorGroupAZ[vm.FlavorGroup][az].subresources = append(
			usageByFlavorGroupAZ[vm.FlavorGroup][az].subresources,
			subresource,
		)
	}

	// Build ResourceUsageReport for all flavor groups (not just those with fixed ratio)
	for flavorGroupName := range flavorGroups {
		// All flavor groups are included in usage reporting.

		// === 1. RAM Resource ===
		ramResourceName := liquid.ResourceName(ResourceNameRAM(flavorGroupName))
		ramPerAZ := make(map[liquid.AvailabilityZone]*liquid.AZResourceUsageReport)
		for _, az := range allAZs {
			ramPerAZ[az] = &liquid.AZResourceUsageReport{
				Usage:        0,
				Subresources: []liquid.Subresource{},
			}
		}
		if azData, exists := usageByFlavorGroupAZ[flavorGroupName]; exists {
			for az, data := range azData {
				if _, known := ramPerAZ[az]; !known {
					ramPerAZ[az] = &liquid.AZResourceUsageReport{}
				}
				ramPerAZ[az].Usage = data.ramUsage
				ramPerAZ[az].PhysicalUsage = Some(data.ramUsage) // No overcommit for RAM
				// Subresources are only on instances resource
			}
		}
		resources[ramResourceName] = &liquid.ResourceUsageReport{
			PerAZ: ramPerAZ,
		}

		// === 2. Cores Resource ===
		coresResourceName := liquid.ResourceName(ResourceNameCores(flavorGroupName))
		coresPerAZ := make(map[liquid.AvailabilityZone]*liquid.AZResourceUsageReport)
		for _, az := range allAZs {
			coresPerAZ[az] = &liquid.AZResourceUsageReport{
				Usage:        0,
				Subresources: []liquid.Subresource{},
			}
		}
		if azData, exists := usageByFlavorGroupAZ[flavorGroupName]; exists {
			for az, data := range azData {
				if _, known := coresPerAZ[az]; !known {
					coresPerAZ[az] = &liquid.AZResourceUsageReport{}
				}
				coresPerAZ[az].Usage = data.coresUsage
				coresPerAZ[az].PhysicalUsage = Some(data.coresUsage) // No overcommit for cores
				// Subresources are only on instances resource
			}
		}
		resources[coresResourceName] = &liquid.ResourceUsageReport{
			PerAZ: coresPerAZ,
		}

		// === 3. Instances Resource ===
		instancesResourceName := liquid.ResourceName(ResourceNameInstances(flavorGroupName))
		instancesPerAZ := make(map[liquid.AvailabilityZone]*liquid.AZResourceUsageReport)
		for _, az := range allAZs {
			instancesPerAZ[az] = &liquid.AZResourceUsageReport{
				Usage:        0,
				Subresources: []liquid.Subresource{},
			}
		}
		if azData, exists := usageByFlavorGroupAZ[flavorGroupName]; exists {
			for az, data := range azData {
				if _, known := instancesPerAZ[az]; !known {
					instancesPerAZ[az] = &liquid.AZResourceUsageReport{}
				}
				instancesPerAZ[az].Usage = data.instanceCount
				instancesPerAZ[az].PhysicalUsage = Some(data.instanceCount)
				instancesPerAZ[az].Subresources = data.subresources // VM details on instances resource
			}
		}
		resources[instancesResourceName] = &liquid.ResourceUsageReport{
			PerAZ: instancesPerAZ,
		}
	}

	return liquid.ServiceUsageReport{
		InfoVersion: infoVersion,
		Resources:   resources,
	}
}

// buildVMAttributes creates the attributes map for a VM subresource.
// Follows the liquid-nova format with nested flavor structure.
func buildVMAttributes(vm VMUsageInfo, commitmentID string) map[string]any {
	flavor := map[string]any{
		"name":     vm.FlavorName,
		"vcpu":     vm.VCPUs,
		"ram_mib":  vm.MemoryMB,
		"disk_gib": vm.DiskGB,
	}
	if vm.VideoRAMMiB != nil {
		flavor["video_ram_mib"] = *vm.VideoRAMMiB
	}

	result := map[string]any{
		"status":   vm.Status,
		"metadata": map[string]string{},
		"tags":     []string{},
		"flavor":   flavor,
		"os_type":  "",
	}

	// Add commitment_id - nil for PAYG, string for committed
	if commitmentID != "" {
		result["commitment_id"] = commitmentID
	} else {
		result["commitment_id"] = nil
	}

	return result
}

// countCommitmentStates returns the total number of commitments across all az:flavorGroup keys.
func countCommitmentStates(m map[string][]*CommitmentStateWithUsage) int {
	count := 0
	for _, list := range m {
		count += len(list)
	}
	return count
}

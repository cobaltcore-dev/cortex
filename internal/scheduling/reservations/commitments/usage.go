// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"sort"
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
	AZ            string
	Hypervisor    string
	CreatedAt     time.Time
	UsageMultiple uint64            // Memory in multiples of smallest flavor in the group
	Metadata      map[string]string // Server metadata from Nova
	Tags          []string          // Server tags from Nova
	OSType        string            // OS type from OSTypeProber (for billing)
}

// flavorAttributes represents flavor information for a VM subresource.
// Matches the format used by liquid-nova for consistency.
type flavorAttributes struct {
	Name           string  `json:"name"`
	VCPUs          uint64  `json:"vcpu"`
	MemoryMiB      uint64  `json:"ram_mib"`
	DiskGiB        uint64  `json:"disk_gib"`
	VideoMemoryMiB *uint64 `json:"video_ram_mib,omitempty"` // Not available yet
}

// subresourceAttributes is the Attributes payload for a VM subresource.
// Matches the format used by liquid-nova for consistency.
type subresourceAttributes struct {
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`
	Tags     []string          `json:"tags"`
	Flavor   flavorAttributes  `json:"flavor"`
	OSType   string            `json:"os_type"` // Not available yet, left empty
}

// UsageCalculator computes usage reports for Limes LIQUID API.
type UsageCalculator struct {
	client     client.Client
	novaClient UsageNovaClient
}

// NewUsageCalculator creates a new UsageCalculator instance.
func NewUsageCalculator(client client.Client, novaClient UsageNovaClient) *UsageCalculator {
	return &UsageCalculator{
		client:     client,
		novaClient: novaClient,
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

// getProjectVMs retrieves all VMs for a project from Nova and enriches them with flavor group info.
func (c *UsageCalculator) getProjectVMs(
	ctx context.Context,
	log logr.Logger,
	projectID string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
) ([]VMUsageInfo, error) {

	if c.novaClient == nil {
		log.Info("Nova client not configured - returning empty VM list", "projectID", projectID)
		return []VMUsageInfo{}, nil
	}

	// Query VMs from Nova
	servers, err := c.novaClient.ListProjectServers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list servers from Nova: %w", err)
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

	// Convert to VMUsageInfo
	var vms []VMUsageInfo
	for _, server := range servers {
		// Parse creation time (Nova returns ISO 8601/RFC3339 format)
		createdAt, err := time.Parse(time.RFC3339, server.Created)
		if err != nil {
			log.V(1).Info("failed to parse server creation time, using zero time",
				"server", server.ID, "created", server.Created, "error", err.Error())
			createdAt = time.Time{}
		}

		// Determine flavor group
		flavorGroup := flavorToGroup[server.FlavorName]

		// Calculate usage multiple (memory in units of smallest flavor)
		var usageMultiple uint64
		if smallestMem := flavorToSmallestMemory[server.FlavorName]; smallestMem > 0 {
			usageMultiple = (server.FlavorRAM + smallestMem - 1) / smallestMem // Round up
		}

		// Normalize AZ - empty or unknown AZs become "unknown" (consistent with limes liquid-nova)
		normalizedAZ := liquid.NormalizeAZ(server.AvailabilityZone, allAZs)

		vm := VMUsageInfo{
			UUID:          server.ID,
			Name:          server.Name,
			FlavorName:    server.FlavorName,
			FlavorGroup:   flavorGroup,
			Status:        server.Status,
			MemoryMB:      server.FlavorRAM,
			VCPUs:         server.FlavorVCPUs,
			DiskGB:        server.FlavorDisk,
			AZ:            string(normalizedAZ),
			Hypervisor:    server.Hypervisor,
			CreatedAt:     createdAt,
			UsageMultiple: usageMultiple,
			Metadata:      server.Metadata,
			Tags:          server.Tags,
			OSType:        server.OSType,
		}

		vms = append(vms, vm)
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
// Only flavor groups that accept commitments are included in the report.
// For each flavor group, three resources are reported: _ram, _cores, _instances.
func (c *UsageCalculator) buildUsageResponse(
	vms []VMUsageInfo,
	vmAssignments map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
	infoVersion int64,
) liquid.ServiceUsageReport {
	// Initialize resources map for flavor groups that accept commitments
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

	// Build ResourceUsageReport for each flavor group that accepts commitments
	for flavorGroupName, groupData := range flavorGroups {
		// Only report usage for flavor groups that accept commitments
		if !FlavorGroupAcceptsCommitments(&groupData) {
			continue
		}

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
	// Build metadata map (never nil for JSON)
	metadata := vm.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	// Build tags slice (never nil for JSON)
	tags := vm.Tags
	if tags == nil {
		tags = []string{}
	}

	attributes := subresourceAttributes{
		Status:   vm.Status,
		Metadata: metadata,
		Tags:     tags,
		Flavor: flavorAttributes{
			Name:      vm.FlavorName,
			VCPUs:     vm.VCPUs,
			MemoryMiB: vm.MemoryMB,
			DiskGiB:   vm.DiskGB,
			// VideoMemoryMiB: nil - not available yet
		},
		OSType: vm.OSType,
	}

	// Convert to map[string]any and add extra fields
	result := map[string]any{
		"status":   attributes.Status,
		"metadata": attributes.Metadata,
		"tags":     attributes.Tags,
		"flavor": map[string]any{
			"name":     attributes.Flavor.Name,
			"vcpu":     attributes.Flavor.VCPUs,
			"ram_mib":  attributes.Flavor.MemoryMiB,
			"disk_gib": attributes.Flavor.DiskGiB,
			// video_ram_mib omitted when nil
		},
		"os_type": vm.OSType,
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

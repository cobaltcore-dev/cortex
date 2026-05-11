// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/sapcc/go-api-declarations/liquid"
	. "go.xyrillian.de/gg/option"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UsageDBClient is the minimal interface for querying VM usage data from Postgres.
type UsageDBClient interface {
	// ListProjectVMs returns all VMs for a project with their flavor data.
	ListProjectVMs(ctx context.Context, projectID string) ([]VMRow, error)
}

// VMRow is the result of a joined server+flavor+image query from Postgres.
type VMRow struct {
	ID           string
	Name         string
	Status       string
	Created      string
	AZ           string
	Hypervisor   string
	FlavorName   string
	FlavorRAM    uint64
	FlavorVCPUs  uint64
	FlavorDisk   uint64
	FlavorExtras string // JSON string of flavor extra_specs
	OSType       string // pre-computed from Glance image properties; "unknown" when not found
}

// CommitmentStateWithUsage extends CommitmentState with usage tracking for billing calculations.
// Used by the report-usage API to track remaining capacity during VM-to-commitment assignment.
type CommitmentStateWithUsage struct {
	CommitmentState
	// RemainingMemoryBytes is the uncommitted capacity left for VM assignment
	RemainingMemoryBytes int64
	// AssignedInstances tracks which VM instances have been assigned to this commitment
	AssignedInstances []string
	// UsedVCPUs is the total vCPU count of assigned VM instances
	UsedVCPUs int64
}

// NewCommitmentStateWithUsage creates a CommitmentStateWithUsage from a CommitmentState.
func NewCommitmentStateWithUsage(state *CommitmentState) *CommitmentStateWithUsage {
	return &CommitmentStateWithUsage{
		CommitmentState:      *state,
		RemainingMemoryBytes: state.TotalMemoryBytes,
		AssignedInstances:    []string{},
	}
}

// AssignVM attempts to assign a VM to this commitment if there's enough capacity.
// Returns true if the VM was assigned, false if not enough capacity.
func (c *CommitmentStateWithUsage) AssignVM(vmUUID string, vmMemoryBytes, vCPUs int64) bool {
	if c.RemainingMemoryBytes >= vmMemoryBytes {
		c.RemainingMemoryBytes -= vmMemoryBytes
		c.UsedVCPUs += vCPUs
		c.AssignedInstances = append(c.AssignedInstances, vmUUID)
		return true
	}
	return false
}

// HasRemainingCapacity returns true if the commitment has any remaining capacity.
func (c *CommitmentStateWithUsage) HasRemainingCapacity() bool {
	return c.RemainingMemoryBytes > 0
}

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
	OSType        string  // pre-computed from Glance image; "unknown" for volume-booted or unmapped images
	AZ            string
	Hypervisor    string
	CreatedAt     time.Time
	UsageMultiple uint64 // RAM in the group's declared unit: slot count (fixed-ratio) or GiB (variable-ratio)
}

// UsageCalculator computes usage reports for Limes LIQUID API.
type UsageCalculator struct {
	client  client.Client
	usageDB UsageDBClient
	config  APIConfig
}

// NewUsageCalculator creates a new UsageCalculator instance.
func NewUsageCalculator(client client.Client, usageDB UsageDBClient, config APIConfig) *UsageCalculator {
	return &UsageCalculator{
		client:  client,
		usageDB: usageDB,
		config:  config,
	}
}

// CalculateUsage computes the usage report for a specific project.
// VM-to-commitment assignment is read from CommittedResource CRD status (pre-computed by the
// UsageReconciler). If a CR has no usage status yet, its VMs appear as PAYG until the first
// reconcile completes (within one CooldownInterval).
func (c *UsageCalculator) CalculateUsage(
	ctx context.Context,
	log logr.Logger,
	projectID string,
	allAZs []liquid.AvailabilityZone,
) (liquid.ServiceUsageReport, error) {

	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: c.client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to get flavor groups: %w", err)
	}

	var infoVersion int64 = -1
	if knowledgeCRD, err := knowledge.Get(ctx); err == nil && knowledgeCRD != nil && !knowledgeCRD.Status.LastContentChange.IsZero() {
		infoVersion = knowledgeCRD.Status.LastContentChange.Unix()
	}

	vmAssignments, err := c.BuildVMAssignmentsFromStatus(ctx, projectID)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to read VM assignments from CRD status: %w", err)
	}

	// Fetch all per-AZ ProjectQuota CRDs for this project to read quota values.
	// May be empty if Limes has not pushed quota yet — in that case quota defaults to infinite.
	// Each CRD holds quota for one AZ; we build a combined map[resourceName][az] = quota.
	var pqList v1alpha1.ProjectQuotaList
	var quotaByResourceAZ map[string]map[string]int64
	if err := c.client.List(ctx, &pqList, client.MatchingFields{idxProjectQuotaByProjectID: projectID}); err == nil && len(pqList.Items) > 0 {
		quotaByResourceAZ = buildCombinedQuotaMap(pqList.Items)
	}

	vms, err := getProjectVMs(ctx, c.usageDB, log, projectID, flavorGroups, allAZs)
	if err != nil {
		return liquid.ServiceUsageReport{}, fmt.Errorf("failed to get project VMs: %w", err)
	}

	report := c.buildUsageResponse(vms, vmAssignments, flavorGroups, allAZs, infoVersion, quotaByResourceAZ, c.config)

	assignedToCommitments := 0
	for _, vm := range vms {
		if vmAssignments[vm.UUID] != "" {
			assignedToCommitments++
		}
	}
	log.Info("completed usage report",
		"projectID", projectID,
		"vmCount", len(vms),
		"assignedToCommitments", assignedToCommitments,
		"payg", len(vms)-assignedToCommitments,
		"resources", len(report.Resources))

	return report, nil
}

// BuildVMAssignmentsFromStatus reads pre-computed VM-to-commitment assignments from
// CommittedResource CRD status. Returns a map of vmUUID → commitmentUUID (empty string = PAYG).
// This is the read path that replaces the inline assignment algorithm in the usage API.
func (c *UsageCalculator) BuildVMAssignmentsFromStatus(ctx context.Context, projectID string) (map[string]string, error) {
	var crList v1alpha1.CommittedResourceList
	if err := c.client.List(ctx, &crList, client.MatchingFields{idxCommittedResourceByProjectID: projectID}); err != nil {
		return nil, fmt.Errorf("failed to list CommittedResources: %w", err)
	}
	assignments := make(map[string]string)
	for _, cr := range crList.Items {
		for _, vmUUID := range cr.Status.AssignedInstances {
			assignments[vmUUID] = cr.Spec.CommitmentUUID
		}
	}
	return assignments, nil
}

// azFlavorGroupKey creates a deterministic key for az:flavorGroup lookups.
func azFlavorGroupKey(az, flavorGroup string) string {
	return az + ":" + flavorGroup
}

// buildCommitmentCapacityMap builds a map of az:flavorGroup -> list of CommitmentStateWithUsage
// from CommittedResource CRD status (AcceptedSpec). Using the accepted spec snapshot gives the
// billing-perspective capacity — what was confirmed — rather than the potentially-mutated current spec.
func buildCommitmentCapacityMap(
	ctx context.Context,
	k8sClient client.Client,
	log logr.Logger,
	projectID string,
) (map[string][]*CommitmentStateWithUsage, error) {

	var allCRs v1alpha1.CommittedResourceList
	if err := k8sClient.List(ctx, &allCRs, client.MatchingFields{idxCommittedResourceByProjectID: projectID}); err != nil {
		return nil, fmt.Errorf("failed to list CommittedResources: %w", err)
	}

	now := time.Now()
	result := make(map[string][]*CommitmentStateWithUsage)
	for _, cr := range allCRs.Items {
		if cr.Status.AcceptedSpec == nil {
			log.V(1).Info("skipping CR with no accepted spec", "cr", cr.Name)
			continue
		}
		// Use AcceptedSpec.State so sibling CRs whose spec is mid-transition (e.g. syncer just
		// wrote expired before the CR controller accepted it) don't lose capacity prematurely.
		if cr.Status.AcceptedSpec.State != v1alpha1.CommitmentStatusConfirmed && cr.Status.AcceptedSpec.State != v1alpha1.CommitmentStatusGuaranteed {
			continue
		}

		// Build state from the accepted spec snapshot so capacity always reflects
		// what was confirmed, not the potentially-mutated current spec.
		tempCR := v1alpha1.CommittedResource{Spec: *cr.Status.AcceptedSpec}
		state, err := FromCommittedResource(tempCR)
		if err != nil {
			log.Error(err, "skipping CR with invalid accepted spec", "cr", cr.Name)
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
func getProjectVMs(
	ctx context.Context,
	usageDB UsageDBClient,
	log logr.Logger,
	projectID string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
) ([]VMUsageInfo, error) {

	if usageDB == nil {
		return nil, errors.New("usage DB client not configured")
	}

	rows, err := usageDB.ListProjectVMs(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs from Postgres: %w", err)
	}

	// Build flavor name -> flavor group lookup
	flavorToGroup := make(map[string]string)
	for groupName, group := range flavorGroups {
		for _, flavor := range group.Flavors {
			flavorToGroup[flavor.Name] = groupName
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

		// Compute usage in the unit declared by the info endpoint for this group:
		//   - fixed-ratio: slot count (FlavorRAM / smallest.MemoryMB); exact since flavors are integer multiples
		//   - variable-ratio or unknown: GiB, with +16 MiB to round up video-RAM-adjusted values
		var usageMultiple uint64
		if row.FlavorRAM > 0 {
			if fg, ok := flavorGroups[flavorGroup]; ok && fg.RamCoreRatio != nil && fg.SmallestFlavor.MemoryMB > 0 {
				usageMultiple = row.FlavorRAM / fg.SmallestFlavor.MemoryMB
			} else {
				usageMultiple = (row.FlavorRAM + 16) / 1024
			}
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
			OSType:        row.OSType,
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
// Mutates each CommitmentStateWithUsage in place: AssignedInstances and RemainingMemoryBytes are updated.
// VMs that don't fit any commitment are left unassigned (PAYG).
func assignVMsToCommitments(
	vms []VMUsageInfo,
	commitmentsByAZFlavorGroup map[string][]*CommitmentStateWithUsage,
) {

	for _, vm := range vms {
		key := azFlavorGroupKey(vm.AZ, vm.FlavorGroup)
		for _, commitment := range commitmentsByAZFlavorGroup[key] {
			vmMemoryBytes := int64(vm.MemoryMB) * 1024 * 1024                 //nolint:gosec // VM memory from Nova, realistically bounded
			if commitment.AssignVM(vm.UUID, vmMemoryBytes, int64(vm.VCPUs)) { //nolint:gosec // VCPUs from Nova, realistically bounded
				break
			}
		}
	}
}

// azUsageData aggregates usage data for a specific flavor group and AZ.
type azUsageData struct {
	ramUsage      uint64               // RAM usage in multiples of smallest flavor
	coresUsage    uint64               // Total vCPU count
	instanceCount uint64               // Number of VMs
	subresources  []liquid.Subresource // VM details for subresource reporting
}

// buildCombinedQuotaMap aggregates per-AZ ProjectQuota CRDs into a combined lookup map.
// Returns quotaByResourceAZ[resourceName][az] = quota value.
func buildCombinedQuotaMap(pqs []v1alpha1.ProjectQuota) map[string]map[string]int64 {
	result := make(map[string]map[string]int64)
	for _, pq := range pqs {
		az := pq.Spec.AvailabilityZone
		for resourceName, quota := range pq.Spec.Quota {
			if result[resourceName] == nil {
				result[resourceName] = make(map[string]int64)
			}
			result[resourceName][az] = quota
		}
	}
	return result
}

// buildUsageResponse constructs the Liquid API ServiceUsageReport.
// All flavor groups are included in the report; commitment assignment only applies
// to groups with fixed RAM/core ratio (those that accept commitments).
// For each flavor group, three resources are reported: _ram, _cores, _instances.
// quotaByResourceAZ is a combined map[resourceName][az] = quota from all per-AZ ProjectQuota CRDs.
func (c *UsageCalculator) buildUsageResponse(
	vms []VMUsageInfo,
	vmAssignments map[string]string,
	flavorGroups map[string]compute.FlavorGroupFeature,
	allAZs []liquid.AvailabilityZone,
	infoVersion int64,
	quotaByResourceAZ map[string]map[string]int64,
	config APIConfig,
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
			Name:       vm.Name,
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
		// Include per-AZ quota for AZSeparatedTopology resources — same condition as info.go.
		ramHasAZQuota := config.ResourceConfigForGroup(flavorGroupName).RAM.HandlesCommitments
		for _, az := range allAZs {
			report := &liquid.AZResourceUsageReport{
				Usage:        0,
				Subresources: []liquid.Subresource{},
			}
			if ramHasAZQuota {
				quota := int64(-1) // default: infinite
				if quotaByResourceAZ != nil {
					if azMap, ok := quotaByResourceAZ[string(ramResourceName)]; ok {
						if q, ok := azMap[string(az)]; ok {
							quota = q
						}
					}
				}
				report.Quota = Some(quota)
			}
			ramPerAZ[az] = report
		}
		if azData, exists := usageByFlavorGroupAZ[flavorGroupName]; exists {
			for az, data := range azData {
				if _, known := ramPerAZ[az]; !known {
					continue // skip VMs in AZs not in allAZs
				}
				ramPerAZ[az].Usage = data.ramUsage
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
					continue // skip VMs in AZs not in allAZs
				}
				coresPerAZ[az].Usage = data.coresUsage
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
					continue // skip VMs in AZs not in allAZs
				}
				instancesPerAZ[az].Usage = data.instanceCount
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
		"status":  vm.Status,
		"flavor":  flavor,
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

// dbUsageClient implements UsageDBClient using a lazy-connecting PostgresReader.
type dbUsageClient struct {
	k8sClient      client.Client
	datasourceName string
	mu             sync.Mutex
	reader         *external.PostgresReader
}

// NewDBUsageClient creates a UsageDBClient that lazily connects to Postgres via the named Datasource CRD.
func NewDBUsageClient(k8sClient client.Client, datasourceName string) UsageDBClient {
	return &dbUsageClient{k8sClient: k8sClient, datasourceName: datasourceName}
}

func (c *dbUsageClient) getReader(ctx context.Context) (*external.PostgresReader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reader != nil {
		return c.reader, nil
	}
	reader, err := external.NewPostgresReader(ctx, c.k8sClient, c.datasourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to datasource %s: %w", c.datasourceName, err)
	}
	c.reader = reader
	return reader, nil
}

// vmQueryRow is the scan target for the server+flavor+image JOIN query.
type vmQueryRow struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Status       string `db:"status"`
	Created      string `db:"created"`
	AZ           string `db:"az"`
	Hypervisor   string `db:"hypervisor"`
	FlavorName   string `db:"flavor_name"`
	FlavorRAM    uint64 `db:"flavor_ram"`
	FlavorVCPUs  uint64 `db:"flavor_vcpus"`
	FlavorDisk   uint64 `db:"flavor_disk"`
	FlavorExtras string `db:"flavor_extras"`
	OSType       string `db:"os_type"`
}

// ListProjectVMs returns all VMs for a project joined with their flavor data from Postgres.
func (c *dbUsageClient) ListProjectVMs(ctx context.Context, projectID string) ([]VMRow, error) {
	reader, err := c.getReader(ctx)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			s.id, s.name, s.status, s.created,
			s.os_ext_az_availability_zone        AS az,
			s.os_ext_srv_attr_hypervisor_hostname AS hypervisor,
			s.flavor_name,
			COALESCE(f.ram, 0)          AS flavor_ram,
			COALESCE(f.vcpus, 0)        AS flavor_vcpus,
			COALESCE(f.disk, 0)         AS flavor_disk,
			COALESCE(f.extra_specs, '') AS flavor_extras,
			COALESCE(NULLIF(i.os_type, ''), 'unknown') AS os_type
		FROM ` + nova.Server{}.TableName() + ` s
		LEFT JOIN ` + nova.Flavor{}.TableName() + ` f ON f.name = s.flavor_name
		LEFT JOIN ` + nova.Image{}.TableName() + ` i ON i.id = s.image_ref
		WHERE s.tenant_id = $1`

	var rows []vmQueryRow
	if err := reader.Select(ctx, &rows, query, projectID); err != nil {
		return nil, fmt.Errorf("failed to query VMs for project %s: %w", projectID, err)
	}

	result := make([]VMRow, len(rows))
	for i, r := range rows {
		result[i] = VMRow(r)
	}
	return result, nil
}

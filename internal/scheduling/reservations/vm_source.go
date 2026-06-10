// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var vmSourceLog = logf.Log.WithName("vm-source")

// VM represents a virtual machine managed by the reservation system.
type VM struct {
	// UUID is the unique identifier of the VM.
	UUID string
	// Name is the display name of the VM in Nova.
	Name string
	// Status is the Nova status of the VM (e.g. ACTIVE, SHUTOFF).
	Status string
	// FlavorName is the name of the flavor used by the VM.
	FlavorName string
	// ProjectID is the OpenStack project ID that owns the VM.
	ProjectID string
	// CurrentHypervisor is the hypervisor where the VM is currently running.
	CurrentHypervisor string
	// AvailabilityZone is the availability zone where the VM is located.
	AvailabilityZone string
	// CreatedAt is the ISO 8601 timestamp when the VM was created in Nova.
	CreatedAt string
	// Resources contains the VM's resource allocations (e.g., "memory", "vcpus").
	Resources map[string]resource.Quantity
	// FlavorExtraSpecs contains the flavor's extra specifications.
	FlavorExtraSpecs map[string]string
	// DiskGB is the flavor's root disk size in GiB.
	DiskGB uint64
	// OSType is the operating system type pre-computed from Glance image properties.
	// "unknown" when not found or for volume-booted instances.
	OSType string
}

// VMSource provides VMs managed by the reservation system.
// This interface allows swapping the implementation when a VM CRD arrives.
type VMSource interface {
	// ListVMs returns all VMs across all projects.
	ListVMs(ctx context.Context) ([]VM, error)
	// ListVMsByProject returns all VMs belonging to a specific project.
	ListVMsByProject(ctx context.Context, projectID string) ([]VM, error)
	// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
	// If trustHypervisorLocation is true, uses hypervisor CRD as source of truth for VM location.
	// If trustHypervisorLocation is false, uses postgres as source of truth but filters to VMs on known hypervisors.
	ListVMsOnHypervisors(ctx context.Context, hypervisorList *hv1.HypervisorList, trustHypervisorLocation bool) ([]VM, error)
	// GetVM returns a specific VM by UUID.
	// Returns nil, nil if the VM is not found.
	GetVM(ctx context.Context, vmUUID string) (*VM, error)
	// IsServerActive returns true if the server exists in the servers table and is not DELETED.
	IsServerActive(ctx context.Context, vmUUID string) (bool, error)
	// GetDeletedVMInfo returns metadata about a deleted VM (from deleted_servers table).
	// Returns nil, nil if not found.
	GetDeletedVMInfo(ctx context.Context, vmUUID string) (*DeletedVMInfo, error)
}

// DeletedVMInfo contains the metadata needed to compute resource decrements for a deleted VM.
type DeletedVMInfo struct {
	ProjectID        string
	AvailabilityZone string
	FlavorName       string
	RAMMiB           uint64
	VCPUs            uint64
}

// DBVMSource implements VMSource by reading directly from the database.
type DBVMSource struct {
	NovaReader external.NovaReaderInterface
}

// NewDBVMSource creates a new DBVMSource.
func NewDBVMSource(novaReader external.NovaReaderInterface) *DBVMSource {
	return &DBVMSource{NovaReader: novaReader}
}

// ListVMs returns all VMs across all projects by joining server and flavor data.
func (s *DBVMSource) ListVMs(ctx context.Context) ([]VM, error) {
	servers, err := s.NovaReader.GetAllServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	flavors, err := s.NovaReader.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	type flavorData struct {
		VCPUs      uint64
		RAM        uint64
		Disk       uint64
		ExtraSpecs string
	}
	flavorByName := make(map[string]flavorData, len(flavors))
	for _, f := range flavors {
		flavorByName[f.Name] = flavorData{VCPUs: f.VCPUs, RAM: f.RAM, Disk: f.Disk, ExtraSpecs: f.ExtraSpecs}
	}

	var skippedNoHost, skippedUnknownFlavor int
	unknownFlavors := make(map[string]int)

	vms := make([]VM, 0, len(servers))
	for _, server := range servers {
		if server.OSEXTSRVATTRHost == "" {
			skippedNoHost++
			continue
		}
		flavor, ok := flavorByName[server.FlavorName]
		if !ok {
			skippedUnknownFlavor++
			unknownFlavors[server.FlavorName]++
			continue
		}
		resources := map[string]resource.Quantity{
			"vcpus":  *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),        //nolint:gosec
			"memory": *resource.NewQuantity(int64(flavor.RAM)*1024*1024, resource.BinarySI), //nolint:gosec
		}
		vms = append(vms, VM{
			UUID:              server.ID,
			Name:              server.Name,
			Status:            server.Status,
			FlavorName:        server.FlavorName,
			ProjectID:         server.TenantID,
			CurrentHypervisor: server.OSEXTSRVATTRHost,
			AvailabilityZone:  server.OSEXTAvailabilityZone,
			CreatedAt:         server.Created,
			Resources:         resources,
			FlavorExtraSpecs:  parseExtraSpecs(flavor.ExtraSpecs),
			DiskGB:            flavor.Disk,
			OSType:            normalizeOSType(server.OSType),
		})
	}

	vmSourceLog.V(1).Info("ListVMs filtering statistics",
		"totalServersInDB", len(servers),
		"skippedNoHost", skippedNoHost,
		"skippedUnknownFlavor", skippedUnknownFlavor,
		"totalFlavorsInDB", len(flavors),
		"returnedVMs", len(vms))
	if len(unknownFlavors) > 0 {
		vmSourceLog.V(1).Info("ListVMs unknown flavors", "unknownFlavors", unknownFlavors)
	}

	return vms, nil
}

// ListVMsByProject returns all VMs for a specific project, querying only that project's servers.
func (s *DBVMSource) ListVMsByProject(ctx context.Context, projectID string) ([]VM, error) {
	servers, err := s.NovaReader.GetServersByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get servers for project: %w", err)
	}

	flavors, err := s.NovaReader.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	type flavorData struct {
		VCPUs      uint64
		RAM        uint64
		Disk       uint64
		ExtraSpecs string
	}
	flavorByName := make(map[string]flavorData, len(flavors))
	for _, f := range flavors {
		flavorByName[f.Name] = flavorData{VCPUs: f.VCPUs, RAM: f.RAM, Disk: f.Disk, ExtraSpecs: f.ExtraSpecs}
	}

	vms := make([]VM, 0, len(servers))
	for _, server := range servers {
		if server.OSEXTSRVATTRHost == "" {
			continue
		}
		flavor, ok := flavorByName[server.FlavorName]
		if !ok {
			continue
		}
		resources := map[string]resource.Quantity{
			"vcpus":  *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),        //nolint:gosec
			"memory": *resource.NewQuantity(int64(flavor.RAM)*1024*1024, resource.BinarySI), //nolint:gosec
		}
		vms = append(vms, VM{
			UUID:              server.ID,
			Name:              server.Name,
			Status:            server.Status,
			FlavorName:        server.FlavorName,
			ProjectID:         server.TenantID,
			CurrentHypervisor: server.OSEXTSRVATTRHost,
			AvailabilityZone:  server.OSEXTAvailabilityZone,
			CreatedAt:         server.Created,
			Resources:         resources,
			FlavorExtraSpecs:  parseExtraSpecs(flavor.ExtraSpecs),
			DiskGB:            flavor.Disk,
			OSType:            normalizeOSType(server.OSType),
		})
	}
	return vms, nil
}

// GetVM returns a specific VM by UUID.
// Returns nil, nil if the VM is not found.
func (s *DBVMSource) GetVM(ctx context.Context, vmUUID string) (*VM, error) {
	server, err := s.NovaReader.GetServerByID(ctx, vmUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if server == nil || server.OSEXTSRVATTRHost == "" {
		return nil, nil
	}

	flavor, err := s.NovaReader.GetFlavorByName(ctx, server.FlavorName)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor: %w", err)
	}
	if flavor == nil {
		return nil, nil
	}

	resources := map[string]resource.Quantity{
		"vcpus":  *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),        //nolint:gosec
		"memory": *resource.NewQuantity(int64(flavor.RAM)*1024*1024, resource.BinarySI), //nolint:gosec
	}

	return &VM{
		UUID:              server.ID,
		Name:              server.Name,
		Status:            server.Status,
		FlavorName:        server.FlavorName,
		ProjectID:         server.TenantID,
		CurrentHypervisor: server.OSEXTSRVATTRHost,
		AvailabilityZone:  server.OSEXTAvailabilityZone,
		CreatedAt:         server.Created,
		Resources:         resources,
		FlavorExtraSpecs:  parseExtraSpecs(flavor.ExtraSpecs),
		DiskGB:            flavor.Disk,
		OSType:            normalizeOSType(server.OSType),
	}, nil
}

// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
func (s *DBVMSource) ListVMsOnHypervisors(
	ctx context.Context,
	hypervisorList *hv1.HypervisorList,
	trustHypervisorLocation bool,
) ([]VM, error) {
	vms, err := s.ListVMs(ctx)
	if err != nil {
		return nil, err
	}

	warnUnknownVMsOnHypervisors(hypervisorList, vms)

	if trustHypervisorLocation {
		result := buildVMsFromHypervisors(hypervisorList, vms)
		vmSourceLog.V(1).Info("built VMs from hypervisor instances (TrustHypervisorLocation=true)",
			"count", len(result),
			"knownHypervisors", len(hypervisorList.Items))
		return result, nil
	}

	result := filterVMsOnKnownHypervisors(vms, hypervisorList)
	vmSourceLog.V(1).Info("filtered VMs to those on known hypervisors and in hypervisor instances",
		"count", len(result),
		"knownHypervisors", len(hypervisorList.Items))
	return result, nil
}

// IsServerActive returns true if the server exists in the servers table and is not DELETED.
func (s *DBVMSource) IsServerActive(ctx context.Context, vmUUID string) (bool, error) {
	server, err := s.NovaReader.GetServerByID(ctx, vmUUID)
	if err != nil {
		return false, fmt.Errorf("failed to check server existence: %w", err)
	}
	if server == nil {
		return false, nil
	}
	return server.Status != "DELETED", nil
}

// GetDeletedVMInfo returns metadata about a deleted VM from the deleted_servers table.
func (s *DBVMSource) GetDeletedVMInfo(ctx context.Context, vmUUID string) (*DeletedVMInfo, error) {
	deletedServer, err := s.NovaReader.GetDeletedServerByID(ctx, vmUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deleted server: %w", err)
	}
	if deletedServer == nil {
		return nil, nil
	}

	flavor, err := s.NovaReader.GetFlavorByName(ctx, deletedServer.FlavorName)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor for deleted server: %w", err)
	}
	if flavor == nil {
		return nil, fmt.Errorf("flavor %q not found for deleted server %s", deletedServer.FlavorName, vmUUID)
	}

	return &DeletedVMInfo{
		ProjectID:        deletedServer.TenantID,
		AvailabilityZone: deletedServer.OSEXTAvailabilityZone,
		FlavorName:       deletedServer.FlavorName,
		RAMMiB:           flavor.RAM,
		VCPUs:            flavor.VCPUs,
	}, nil
}

// ============================================================================
// Internal helpers
// ============================================================================

func parseExtraSpecs(extraSpecsJSON string) map[string]string {
	if extraSpecsJSON == "" {
		return make(map[string]string)
	}
	var extraSpecs map[string]string
	if err := json.Unmarshal([]byte(extraSpecsJSON), &extraSpecs); err != nil {
		vmSourceLog.Error(err, "failed to parse flavor extra specs JSON",
			"extraSpecsJSON", truncateString(extraSpecsJSON, 100))
		return make(map[string]string)
	}
	return extraSpecs
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// normalizeOSType returns "unknown" for empty OS type strings.
func normalizeOSType(osType string) string {
	if osType == "" {
		return "unknown"
	}
	return osType
}

func buildVMsFromHypervisors(hypervisorList *hv1.HypervisorList, postgresVMs []VM) []VM {
	vmDataByUUID := make(map[string]VM, len(postgresVMs))
	for _, vm := range postgresVMs {
		vmDataByUUID[vm.UUID] = vm
	}

	var result []VM
	var enrichedCount, notInPostgresCount, duplicateCount int
	seen := make(map[string]string)

	for _, hv := range hypervisorList.Items {
		for _, inst := range hv.Status.Instances {
			if !inst.Active {
				continue
			}
			if firstHypervisor, alreadySeen := seen[inst.ID]; alreadySeen {
				vmSourceLog.Info("duplicate VM UUID on multiple hypervisors, skipping",
					"uuid", inst.ID,
					"hypervisor", hv.Name,
					"firstSeenOn", firstHypervisor)
				duplicateCount++
				continue
			}
			seen[inst.ID] = hv.Name

			pgVM, existsInPostgres := vmDataByUUID[inst.ID]
			if !existsInPostgres {
				notInPostgresCount++
				continue
			}

			vm := VM{
				UUID:              inst.ID,
				Name:              pgVM.Name,
				Status:            pgVM.Status,
				FlavorName:        pgVM.FlavorName,
				ProjectID:         pgVM.ProjectID,
				CurrentHypervisor: hv.Name,
				AvailabilityZone:  pgVM.AvailabilityZone,
				CreatedAt:         pgVM.CreatedAt,
				Resources:         pgVM.Resources,
				FlavorExtraSpecs:  pgVM.FlavorExtraSpecs,
				DiskGB:            pgVM.DiskGB,
				OSType:            pgVM.OSType,
			}
			result = append(result, vm)
			enrichedCount++
		}
	}

	vmSourceLog.V(1).Info("buildVMsFromHypervisors statistics",
		"totalHypervisorInstances", enrichedCount+notInPostgresCount+duplicateCount,
		"enrichedWithPostgresData", enrichedCount,
		"notInPostgres", notInPostgresCount,
		"duplicatesSkipped", duplicateCount)

	return result
}

func filterVMsOnKnownHypervisors(vms []VM, hypervisorList *hv1.HypervisorList) []VM {
	hypervisorSet := make(map[string]bool, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		hypervisorSet[hv.Name] = true
	}

	vmOnHypervisor := make(map[string]bool)
	allVMsOnHypervisors := make(map[string]string)
	totalVMsOnHypervisors := 0
	for _, hv := range hypervisorList.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				key := inst.ID + ":" + hv.Name
				vmOnHypervisor[key] = true
				allVMsOnHypervisors[inst.ID] = hv.Name
				totalVMsOnHypervisors++
			}
		}
	}

	var result []VM
	var filteredUnknownHypervisor, filteredNotInInstances, filteredWrongHypervisor int
	for _, vm := range vms {
		if !hypervisorSet[vm.CurrentHypervisor] {
			filteredUnknownHypervisor++
			continue
		}
		key := vm.UUID + ":" + vm.CurrentHypervisor
		if !vmOnHypervisor[key] {
			if actualHypervisor, exists := allVMsOnHypervisors[vm.UUID]; exists {
				vmSourceLog.V(2).Info("VM claims to be on one hypervisor but is actually on another",
					"vmUUID", vm.UUID,
					"claimedHypervisor", vm.CurrentHypervisor,
					"actualHypervisor", actualHypervisor)
				filteredWrongHypervisor++
			} else {
				filteredNotInInstances++
			}
			continue
		}
		result = append(result, vm)
	}

	totalFiltered := filteredUnknownHypervisor + filteredNotInInstances + filteredWrongHypervisor
	if totalFiltered > 0 {
		vmSourceLog.Info("filterVMsOnKnownHypervisors statistics",
			"inputVMs", len(vms),
			"totalVMsOnHypervisors", totalVMsOnHypervisors,
			"filteredUnknownHypervisor", filteredUnknownHypervisor,
			"filteredNotInInstances", filteredNotInInstances,
			"filteredWrongHypervisor", filteredWrongHypervisor,
			"totalFiltered", totalFiltered,
			"remainingCount", len(result))
	}

	return result
}

func warnUnknownVMsOnHypervisors(hypervisors *hv1.HypervisorList, vms []VM) {
	vmUUIDs := make(map[string]bool, len(vms))
	for _, vm := range vms {
		vmUUIDs[vm.UUID] = true
	}

	hypervisorVMUUIDs := make(map[string]bool)
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				hypervisorVMUUIDs[inst.ID] = true
			}
		}
	}

	vmsOnHypervisorsNotInListVMs := 0
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active && !vmUUIDs[inst.ID] {
				vmSourceLog.Info("WARNING: VM on hypervisor not found in ListVMs - possible data sync issue",
					"vmUUID", inst.ID,
					"vmName", inst.Name,
					"hypervisor", hv.Name)
				vmsOnHypervisorsNotInListVMs++
			}
		}
	}

	vmsInListVMsNotOnHypervisors := 0
	for _, vm := range vms {
		if !hypervisorVMUUIDs[vm.UUID] {
			vmsInListVMsNotOnHypervisors++
		}
	}

	if vmsOnHypervisorsNotInListVMs > 0 {
		vmSourceLog.V(1).Info("VMs on hypervisors not found in ListVMs",
			"count", vmsOnHypervisorsNotInListVMs,
			"hint", "This may indicate a data sync issue between hypervisor operator and nova servers")
	}
	if vmsInListVMsNotOnHypervisors > 0 {
		vmSourceLog.V(1).Info("VMs in ListVMs not found on any hypervisor",
			"count", vmsInListVMsNotOnHypervisors,
			"hint", "This may indicate a data sync issue between nova servers and hypervisor operator")
	}
}


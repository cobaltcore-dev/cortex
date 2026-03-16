// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// VM represents a virtual machine that may need failover reservations.
type VM struct {
	// UUID is the unique identifier of the VM.
	UUID string
	// FlavorName is the name of the flavor used by the VM.
	FlavorName string
	// ProjectID is the OpenStack project ID that owns the VM.
	ProjectID string
	// CurrentHypervisor is the hypervisor where the VM is currently running.
	CurrentHypervisor string
	// AvailabilityZone is the availability zone where the VM is located.
	// This is used to ensure failover reservations are created in the same AZ.
	AvailabilityZone string
	// Resources contains the VM's resource allocations (e.g., "memory", "vcpus").
	Resources map[string]resource.Quantity
	// FlavorExtraSpecs contains the flavor's extra specifications (e.g., traits, capabilities).
	// This is used by filters like filter_has_requested_traits and filter_capabilities.
	FlavorExtraSpecs map[string]string
}

// VMSource provides VMs that may need failover reservations.
// This interface allows swapping the implementation when a VM CRD arrives.
type VMSource interface {
	// ListVMs returns all VMs that might need failover reservations.
	ListVMs(ctx context.Context) ([]VM, error)
	// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
	// If trustHypervisorLocation is true, uses hypervisor CRD as source of truth for VM location.
	// If trustHypervisorLocation is false, uses postgres as source of truth but filters to VMs on known hypervisors.
	// Also logs warnings about data sync issues between postgres and hypervisor CRD.
	ListVMsOnHypervisors(ctx context.Context, hypervisorList *hv1.HypervisorList, trustHypervisorLocation bool) ([]VM, error)
	// GetVM returns a specific VM by UUID.
	// Returns nil, nil if the VM is not found (not an error, just doesn't exist).
	GetVM(ctx context.Context, vmUUID string) (*VM, error)
}

// DBVMSource implements VMSource by reading directly from the database.
// This is the preferred implementation as it avoids the size limitations of Knowledge CRDs.
type DBVMSource struct {
	NovaReader external.NovaReaderInterface
}

// NewDBVMSource creates a new DBVMSource.
func NewDBVMSource(novaReader external.NovaReaderInterface) *DBVMSource {
	return &DBVMSource{NovaReader: novaReader}
}

// ListVMs returns all VMs by joining server and flavor data from the database.
func (s *DBVMSource) ListVMs(ctx context.Context) ([]VM, error) {
	// Fetch all servers
	servers, err := s.NovaReader.GetAllServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	// Fetch all flavors and build a lookup map
	flavors, err := s.NovaReader.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	flavorByName := make(map[string]struct {
		VCPUs      uint64
		RAM        uint64
		ExtraSpecs string
	})
	for _, f := range flavors {
		flavorByName[f.Name] = struct {
			VCPUs      uint64
			RAM        uint64
			ExtraSpecs string
		}{VCPUs: f.VCPUs, RAM: f.RAM, ExtraSpecs: f.ExtraSpecs}
	}

	// Track filtering statistics
	var skippedNoHost, skippedUnknownFlavor int
	unknownFlavors := make(map[string]int)

	// Convert servers to VMs
	vms := make([]VM, 0, len(servers))
	for _, server := range servers {
		// Skip servers without a host (not yet scheduled)
		if server.OSEXTSRVATTRHost == "" {
			skippedNoHost++
			continue
		}

		// Look up flavor resources
		flavor, ok := flavorByName[server.FlavorName]
		if !ok {
			// Skip servers with unknown flavors
			skippedUnknownFlavor++
			unknownFlavors[server.FlavorName]++
			continue
		}

		// Build resources map
		resources := map[string]resource.Quantity{
			"vcpus":  *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),        //nolint:gosec // VCPUs won't overflow int64
			"memory": *resource.NewQuantity(int64(flavor.RAM)*1024*1024, resource.BinarySI), //nolint:gosec // RAM in MB won't overflow int64
		}

		// Parse extra specs from JSON string
		extraSpecs := parseExtraSpecs(flavor.ExtraSpecs)

		vms = append(vms, VM{
			UUID:              server.ID,
			FlavorName:        server.FlavorName,
			ProjectID:         server.TenantID,
			CurrentHypervisor: server.OSEXTSRVATTRHost,
			AvailabilityZone:  server.OSEXTAvailabilityZone,
			Resources:         resources,
			FlavorExtraSpecs:  extraSpecs,
		})
	}

	// Log filtering statistics
	log.Info("ListVMs filtering statistics",
		"totalServersInDB", len(servers),
		"skippedNoHost", skippedNoHost,
		"skippedUnknownFlavor", skippedUnknownFlavor,
		"totalFlavorsInDB", len(flavors),
		"returnedVMs", len(vms))
	if len(unknownFlavors) > 0 {
		log.Info("ListVMs unknown flavors", "unknownFlavors", unknownFlavors)
	}

	return vms, nil
}

// parseExtraSpecs parses a JSON string of extra specs into a map.
// Returns an empty map if the string is empty or invalid.
func parseExtraSpecs(extraSpecsJSON string) map[string]string {
	if extraSpecsJSON == "" {
		return make(map[string]string)
	}
	var extraSpecs map[string]string
	if err := json.Unmarshal([]byte(extraSpecsJSON), &extraSpecs); err != nil {
		// Log error but don't fail - return empty map
		log.Error(err, "failed to parse flavor extra specs JSON",
			"extraSpecsJSON", truncateString(extraSpecsJSON, 100))
		return make(map[string]string)
	}
	return extraSpecs
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetVM returns a specific VM by UUID.
// Returns nil, nil if the VM is not found (not an error, just doesn't exist).
func (s *DBVMSource) GetVM(ctx context.Context, vmUUID string) (*VM, error) {
	// Fetch the server by UUID
	server, err := s.NovaReader.GetServerByID(ctx, vmUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}
	if server == nil {
		// Server not found
		return nil, nil
	}

	// Skip servers without a host (not yet scheduled)
	if server.OSEXTSRVATTRHost == "" {
		return nil, nil
	}

	// Fetch the flavor for this server
	flavor, err := s.NovaReader.GetFlavorByName(ctx, server.FlavorName)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavor: %w", err)
	}
	if flavor == nil {
		// Flavor not found
		return nil, nil
	}

	// Build resources map
	resources := map[string]resource.Quantity{
		"vcpus":  *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),        //nolint:gosec // VCPUs won't overflow int64
		"memory": *resource.NewQuantity(int64(flavor.RAM)*1024*1024, resource.BinarySI), //nolint:gosec // RAM in MB won't overflow int64
	}

	// Parse extra specs from JSON string
	extraSpecs := parseExtraSpecs(flavor.ExtraSpecs)

	return &VM{
		UUID:              server.ID,
		FlavorName:        server.FlavorName,
		ProjectID:         server.TenantID,
		CurrentHypervisor: server.OSEXTSRVATTRHost,
		AvailabilityZone:  server.OSEXTAvailabilityZone,
		Resources:         resources,
		FlavorExtraSpecs:  extraSpecs,
	}, nil
}

// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
// If trustHypervisorLocation is true, uses hypervisor CRD as source of truth for VM location.
// If trustHypervisorLocation is false, uses postgres as source of truth but filters to VMs on known hypervisors.
// Also logs warnings about data sync issues between postgres and hypervisor CRD.
func (s *DBVMSource) ListVMsOnHypervisors(
	ctx context.Context,
	hypervisorList *hv1.HypervisorList,
	trustHypervisorLocation bool,
) ([]VM, error) {
	// Get VMs from postgres
	vms, err := s.ListVMs(ctx)
	if err != nil {
		return nil, err
	}

	// Warn about data sync issues
	warnUnknownVMsOnHypervisors(hypervisorList, vms)

	if trustHypervisorLocation {
		// Use hypervisor CRD as source of truth for VM location
		result := buildVMsFromHypervisors(hypervisorList, vms)
		log.Info("built VMs from hypervisor instances (TrustHypervisorLocation=true)",
			"count", len(result),
			"knownHypervisors", len(hypervisorList.Items))
		return result, nil
	}

	// Use postgres as source of truth, but filter to VMs on known hypervisors
	result := filterVMsOnKnownHypervisors(vms, hypervisorList)
	log.Info("filtered VMs to those on known hypervisors and in hypervisor instances",
		"count", len(result),
		"knownHypervisors", len(hypervisorList.Items))
	return result, nil
}

// ============================================================================
// VM/Hypervisor Processing (internal helpers)
// ============================================================================

// buildVMsFromHypervisors builds VMs from hypervisor instances, using the hypervisor CRD
// as the source of truth for VM location. It enriches VMs with data from postgres (flavor, size, extra specs, AZ).
// This is used when TrustHypervisorLocation is true.
//
// The function:
// 1. Iterates through all hypervisor instances to get VM UUIDs and their actual location
// 2. Looks up each VM in the postgres-sourced vms list to get flavor/size/extra specs/AZ
// 3. Returns VMs that exist in both hypervisor instances AND postgres (need postgres for scheduling data)
// 4. Deduplicates VMs that appear on multiple hypervisors (transient state during live migration)
func buildVMsFromHypervisors(hypervisorList *hv1.HypervisorList, postgresVMs []VM) []VM {
	// Build a map of VM UUID -> VM data from postgres for quick lookup
	vmDataByUUID := make(map[string]VM, len(postgresVMs))
	for _, vm := range postgresVMs {
		vmDataByUUID[vm.UUID] = vm
	}

	var result []VM
	var enrichedCount, notInPostgresCount, duplicateCount int

	// Track seen UUIDs to deduplicate VMs that appear on multiple hypervisors
	// This can happen transiently during live migration
	seen := make(map[string]string) // vmUUID -> first hypervisor seen

	// Iterate through hypervisor instances
	for _, hv := range hypervisorList.Items {
		for _, inst := range hv.Status.Instances {
			if !inst.Active {
				continue
			}

			// Check for duplicate UUIDs (same VM on multiple hypervisors)
			if firstHypervisor, alreadySeen := seen[inst.ID]; alreadySeen {
				log.Info("duplicate VM UUID on multiple hypervisors, skipping",
					"uuid", inst.ID,
					"hypervisor", hv.Name,
					"firstSeenOn", firstHypervisor)
				duplicateCount++
				continue
			}
			seen[inst.ID] = hv.Name

			// Look up VM data from postgres
			pgVM, existsInPostgres := vmDataByUUID[inst.ID]
			if !existsInPostgres {
				// VM is on hypervisor but not in postgres - skip (need postgres for flavor/size)
				notInPostgresCount++
				continue
			}

			// Build VM with hypervisor location but postgres data (including AZ)
			vm := VM{
				UUID:              inst.ID,
				FlavorName:        pgVM.FlavorName,
				ProjectID:         pgVM.ProjectID,
				CurrentHypervisor: hv.Name, // Use hypervisor CRD location, not postgres
				AvailabilityZone:  pgVM.AvailabilityZone,
				Resources:         pgVM.Resources,
				FlavorExtraSpecs:  pgVM.FlavorExtraSpecs,
			}
			result = append(result, vm)
			enrichedCount++
		}
	}

	log.Info("buildVMsFromHypervisors statistics",
		"totalHypervisorInstances", enrichedCount+notInPostgresCount+duplicateCount,
		"enrichedWithPostgresData", enrichedCount,
		"notInPostgres", notInPostgresCount,
		"duplicatesSkipped", duplicateCount)

	return result
}

// filterVMsOnKnownHypervisors filters VMs to only include those that:
// 1. Are running on a known hypervisor
// 2. Are actually listed in that hypervisor's Status.Instances
// This removes VMs that are on hypervisors not managed by the hypervisor operator,
// or VMs that claim to be on a hypervisor but aren't in its instances list (data sync issue).
func filterVMsOnKnownHypervisors(vms []VM, hypervisorList *hv1.HypervisorList) []VM {
	// Build a set of known hypervisors for O(1) lookup
	hypervisorSet := make(map[string]bool, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		hypervisorSet[hv.Name] = true
	}

	// Build a set of VM UUIDs that are actually in hypervisor instances
	// Key: "vmUUID:hypervisorName" to ensure VM is on the correct hypervisor
	vmOnHypervisor := make(map[string]bool)
	// Also track all VMs on hypervisors (regardless of which hypervisor)
	allVMsOnHypervisors := make(map[string]string) // vmUUID -> hypervisorName
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
	var filteredUnknownHypervisor int
	var filteredNotInInstances int
	var filteredWrongHypervisor int
	for _, vm := range vms {
		// Check if hypervisor is known
		if !hypervisorSet[vm.CurrentHypervisor] {
			filteredUnknownHypervisor++
			continue
		}
		// Check if VM is actually in the hypervisor's instances list
		key := vm.UUID + ":" + vm.CurrentHypervisor
		if !vmOnHypervisor[key] {
			// Check if VM is on a different hypervisor
			if actualHypervisor, exists := allVMsOnHypervisors[vm.UUID]; exists {
				log.V(2).Info("VM claims to be on one hypervisor but is actually on another",
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
		log.Info("filterVMsOnKnownHypervisors statistics",
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

// warnUnknownVMsOnHypervisors logs a warning for VMs that are on hypervisors but not in the ListVMs (i.e. nova) result.
// This can indicate a data sync issue between the hypervisor operator and the VM datasource.
func warnUnknownVMsOnHypervisors(hypervisors *hv1.HypervisorList, vms []VM) {
	// Build a set of VM UUIDs from ListVMs
	vmUUIDs := make(map[string]bool, len(vms))
	for _, vm := range vms {
		vmUUIDs[vm.UUID] = true
	}

	// Build a set of VM UUIDs from hypervisors
	hypervisorVMUUIDs := make(map[string]bool)
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				hypervisorVMUUIDs[inst.ID] = true
			}
		}
	}

	// Check each hypervisor's instances - VMs on hypervisors but not in ListVMs
	vmsOnHypervisorsNotInListVMs := 0
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active && !vmUUIDs[inst.ID] {
				log.Info("WARNING: VM on hypervisor not found in ListVMs - possible data sync issue",
					"vmUUID", inst.ID,
					"vmName", inst.Name,
					"hypervisor", hv.Name)
				vmsOnHypervisorsNotInListVMs++
			}
		}
	}

	// Check VMs in ListVMs but not on any hypervisor
	vmsInListVMsNotOnHypervisors := 0
	for _, vm := range vms {
		if !hypervisorVMUUIDs[vm.UUID] {
			vmsInListVMsNotOnHypervisors++
		}
	}

	if vmsOnHypervisorsNotInListVMs > 0 {
		log.Info("WARNING: VMs on hypervisors not found in ListVMs",
			"count", vmsOnHypervisorsNotInListVMs,
			"hint", "This may indicate a data sync issue between hypervisor operator and nova servers")
	}

	if vmsInListVMsNotOnHypervisors > 0 {
		log.Info("WARNING: VMs in ListVMs not found on any hypervisor",
			"count", vmsInListVMsNotOnHypervisors,
			"hint", "This may indicate a data sync issue between nova servers and hypervisor operator")
	}
}

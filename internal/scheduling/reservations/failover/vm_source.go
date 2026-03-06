// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"
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
}

// DBVMSource implements VMSource by reading directly from the database.
// This is the preferred implementation as it avoids the size limitations of Knowledge CRDs.
type DBVMSource struct {
	NovaReader *external.NovaReader
}

// NewDBVMSource creates a new DBVMSource.
func NewDBVMSource(novaReader *external.NovaReader) *DBVMSource {
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

	// Convert servers to VMs
	vms := make([]VM, 0, len(servers))
	for _, server := range servers {
		// Skip servers without a host (not yet scheduled)
		if server.OSEXTSRVATTRHost == "" {
			continue
		}

		// Look up flavor resources
		flavor, ok := flavorByName[server.FlavorName]
		if !ok {
			// Skip servers with unknown flavors
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
			Resources:         resources,
			FlavorExtraSpecs:  extraSpecs,
		})
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

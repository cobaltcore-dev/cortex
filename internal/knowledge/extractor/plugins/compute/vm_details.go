// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// vmDetailsRow represents the raw SQL result row.
type vmDetailsRow struct {
	// UUID of the OpenStack server.
	ServerUUID string `db:"server_uuid"`
	// Name of the flavor assigned to the server.
	FlavorName string `db:"flavor_name"`
	// ProjectID (tenant_id) that owns the server.
	ProjectID string `db:"project_id"`
	// CurrentHost is the compute host where the VM is running.
	CurrentHost string `db:"current_host"`
	// RAM in MB from the flavor (nullable if flavor not found).
	RAM *uint64 `db:"ram"`
	// Number of VCPUs from the flavor (nullable if flavor not found).
	VCPUs *uint64 `db:"vcpus"`
}

// VMDetails represents the extracted knowledge about a VM including
// server information and enriched flavor details.
type VMDetails struct {
	// UUID of the OpenStack server.
	ServerUUID string `json:"server_uuid"`
	// Name of the flavor assigned to the server.
	FlavorName string `json:"flavor_name"`
	// ProjectID (tenant_id) that owns the server.
	ProjectID string `json:"project_id"`
	// CurrentHost is the compute host where the VM is running.
	CurrentHost string `json:"current_host"`
	// Resources contains the VM's resource allocations (e.g., "memory", "vcpus").
	// Memory is stored in bytes, VCPUs as a count.
	Resources map[string]resource.Quantity `json:"resources,omitempty"`
}

type VMDetailsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},  // No options passed through yaml config
		VMDetails, // Feature model
	]
}

//go:embed vm_details.sql
var vmDetailsQuery string

// Extract the VM details from the database by joining servers with flavors.
func (e *VMDetailsExtractor) Extract() ([]plugins.Feature, error) {
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}

	var rows []vmDetailsRow
	if _, err := e.DB.Select(&rows, vmDetailsQuery); err != nil {
		return nil, err
	}

	// Convert rows to VMDetails with Resources map
	vmDetails := make([]VMDetails, len(rows))
	for i, row := range rows {
		vmDetails[i] = VMDetails{
			ServerUUID:  row.ServerUUID,
			FlavorName:  row.FlavorName,
			ProjectID:   row.ProjectID,
			CurrentHost: row.CurrentHost,
			Resources:   make(map[string]resource.Quantity),
		}

		// Add RAM if available (convert MB to bytes for standard resource.Quantity)
		if row.RAM != nil {
			// RAM is in MB, convert to bytes (Mi = mebibytes)
			vmDetails[i].Resources["memory"] = resource.MustParse(
				formatMegabytes(*row.RAM),
			)
		}

		// Add VCPUs if available
		if row.VCPUs != nil {
			vmDetails[i].Resources["vcpus"] = resource.MustParse(
				formatVCPUs(*row.VCPUs),
			)
		}
	}

	return e.Extracted(vmDetails)
}

// formatMegabytes formats megabytes as a resource quantity string.
func formatMegabytes(mb uint64) string {
	return fmt.Sprintf("%dMi", mb)
}

// formatVCPUs formats VCPUs as a resource quantity string.
func formatVCPUs(vcpus uint64) string {
	return strconv.FormatUint(vcpus, 10)
}

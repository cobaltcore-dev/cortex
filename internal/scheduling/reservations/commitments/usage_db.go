// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
)

// dbUsageClient implements UsageDBClient using a *db.DB directly.
type dbUsageClient struct {
	db *db.DB
}

// NewDBUsageClient creates a UsageDBClient backed by the given database connection.
func NewDBUsageClient(database *db.DB) UsageDBClient {
	return &dbUsageClient{db: database}
}

// vmQueryRow is the scan target for the server+flavor JOIN query.
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
}

// ListProjectVMs returns all VMs for a project joined with their flavor data from Postgres.
func (c *dbUsageClient) ListProjectVMs(_ context.Context, projectID string) ([]VMRow, error) {
	query := `
		SELECT
			s.id, s.name, s.status, s.created,
			s.os_ext_az_availability_zone        AS az,
			s.os_ext_srv_attr_hypervisor_hostname AS hypervisor,
			s.flavor_name,
			COALESCE(f.ram, 0)          AS flavor_ram,
			COALESCE(f.vcpus, 0)        AS flavor_vcpus,
			COALESCE(f.disk, 0)         AS flavor_disk,
			COALESCE(f.extra_specs, '') AS flavor_extras
		FROM ` + nova.Server{}.TableName() + ` s
		LEFT JOIN ` + nova.Flavor{}.TableName() + ` f ON f.name = s.flavor_name
		WHERE s.tenant_id = $1`

	var rows []vmQueryRow
	if _, err := c.db.Select(&rows, query, projectID); err != nil {
		return nil, fmt.Errorf("failed to query VMs for project %s: %w", projectID, err)
	}

	result := make([]VMRow, len(rows))
	for i, r := range rows {
		result[i] = VMRow(r)
	}
	return result, nil
}

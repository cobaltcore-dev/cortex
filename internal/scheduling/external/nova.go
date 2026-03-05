// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package external

import (
	"context"
	"fmt"

	nova "github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
)

// NovaReader provides read access to Nova data stored in the database.
// It uses a PostgresReader to connect to the database.
type NovaReader struct {
	*PostgresReader
}

// NewNovaReader creates a new NovaReader from a PostgresReader.
func NewNovaReader(reader *PostgresReader) *NovaReader {
	return &NovaReader{PostgresReader: reader}
}

// GetAllServers returns all Nova servers from the database.
func (r *NovaReader) GetAllServers(ctx context.Context) ([]nova.Server, error) {
	var servers []nova.Server
	query := "SELECT * FROM " + nova.Server{}.TableName()
	if err := r.Select(ctx, &servers, query); err != nil {
		return nil, fmt.Errorf("failed to query servers: %w", err)
	}
	return servers, nil
}

// GetAllFlavors returns all Nova flavors from the database.
func (r *NovaReader) GetAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
	var flavors []nova.Flavor
	query := "SELECT * FROM " + nova.Flavor{}.TableName()
	if err := r.Select(ctx, &flavors, query); err != nil {
		return nil, fmt.Errorf("failed to query flavors: %w", err)
	}
	return flavors, nil
}

// GetAllHypervisors returns all Nova hypervisors from the database.
func (r *NovaReader) GetAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error) {
	var hypervisors []nova.Hypervisor
	query := "SELECT * FROM " + nova.Hypervisor{}.TableName()
	if err := r.Select(ctx, &hypervisors, query); err != nil {
		return nil, fmt.Errorf("failed to query hypervisors: %w", err)
	}
	return hypervisors, nil
}

// GetAllMigrations returns all Nova migrations from the database.
func (r *NovaReader) GetAllMigrations(ctx context.Context) ([]nova.Migration, error) {
	var migrations []nova.Migration
	query := "SELECT * FROM " + nova.Migration{}.TableName()
	if err := r.Select(ctx, &migrations, query); err != nil {
		return nil, fmt.Errorf("failed to query migrations: %w", err)
	}
	return migrations, nil
}

// GetAllAggregates returns all Nova aggregates from the database.
func (r *NovaReader) GetAllAggregates(ctx context.Context) ([]nova.Aggregate, error) {
	var aggregates []nova.Aggregate
	query := "SELECT * FROM " + nova.Aggregate{}.TableName()
	if err := r.Select(ctx, &aggregates, query); err != nil {
		return nil, fmt.Errorf("failed to query aggregates: %w", err)
	}
	return aggregates, nil
}

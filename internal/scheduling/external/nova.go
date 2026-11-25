// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package external

import (
	"context"
	"fmt"

	identity "github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	nova "github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
)

// NovaReaderInterface defines the methods needed to read Nova data.
// This interface allows mocking the NovaReader in tests.
type NovaReaderInterface interface {
	GetAllServers(ctx context.Context) ([]nova.Server, error)
	GetAllFlavors(ctx context.Context) ([]nova.Flavor, error)
	GetServerByID(ctx context.Context, serverID string) (*nova.Server, error)
	GetFlavorByName(ctx context.Context, flavorName string) (*nova.Flavor, error)
	// GetServersByProject returns all servers belonging to a specific project.
	GetServersByProject(ctx context.Context, projectID string) ([]nova.Server, error)
	// GetDeletedServerByID returns a deleted server by its ID from the deleted_servers table.
	// Returns nil, nil if the server is not found in the deleted_servers table.
	GetDeletedServerByID(ctx context.Context, serverID string) (*nova.DeletedServer, error)
	// GetProjectDomainNames returns a map of project ID to its domain name, joining
	// the identity projects and domains tables. Projects whose domain cannot be
	// resolved are omitted.
	GetProjectDomainNames(ctx context.Context) (map[string]string, error)
}

// NovaReader provides read access to Nova data persisted in a store.
type NovaReader struct {
	*StoreReader
}

// NewNovaReader creates a new NovaReader from a StoreReader.
func NewNovaReader(reader *StoreReader) *NovaReader {
	return &NovaReader{StoreReader: reader}
}

// GetAllServers returns all Nova servers.
func (r *NovaReader) GetAllServers(ctx context.Context) ([]nova.Server, error) {
	servers, err := listAll[nova.Server](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}
	return servers, nil
}

// GetAllFlavors returns all Nova flavors.
func (r *NovaReader) GetAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
	flavors, err := listAll[nova.Flavor](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list flavors: %w", err)
	}
	return flavors, nil
}

// GetAllHypervisors returns all Nova hypervisors.
func (r *NovaReader) GetAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error) {
	hypervisors, err := listAll[nova.Hypervisor](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list hypervisors: %w", err)
	}
	return hypervisors, nil
}

// GetAllMigrations returns all Nova migrations.
func (r *NovaReader) GetAllMigrations(ctx context.Context) ([]nova.Migration, error) {
	migrations, err := listAll[nova.Migration](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list migrations: %w", err)
	}
	return migrations, nil
}

// GetAllAggregates returns all Nova aggregates.
func (r *NovaReader) GetAllAggregates(ctx context.Context) ([]nova.Aggregate, error) {
	aggregates, err := listAll[nova.Aggregate](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list aggregates: %w", err)
	}
	return aggregates, nil
}

// GetServersByProject returns all Nova servers belonging to a specific project.
func (r *NovaReader) GetServersByProject(ctx context.Context, projectID string) ([]nova.Server, error) {
	servers, err := r.GetAllServers(ctx)
	if err != nil {
		return nil, err
	}
	var result []nova.Server
	for _, s := range servers {
		if s.TenantID == projectID {
			result = append(result, s)
		}
	}
	return result, nil
}

// GetServerByID returns a Nova server by its ID.
// Returns nil, nil if the server is not found.
func (r *NovaReader) GetServerByID(ctx context.Context, serverID string) (*nova.Server, error) {
	servers, err := r.GetAllServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range servers {
		if s.ID == serverID {
			return &s, nil
		}
	}
	return nil, nil
}

// GetFlavorByName returns a Nova flavor by its name.
// Returns nil, nil if the flavor is not found.
func (r *NovaReader) GetFlavorByName(ctx context.Context, flavorName string) (*nova.Flavor, error) {
	flavors, err := r.GetAllFlavors(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range flavors {
		if f.Name == flavorName {
			return &f, nil
		}
	}
	return nil, nil
}

// GetDeletedServerByID returns a deleted Nova server by its ID.
// Returns nil, nil if the server is not found among the deleted servers.
func (r *NovaReader) GetDeletedServerByID(ctx context.Context, serverID string) (*nova.DeletedServer, error) {
	servers, err := listAll[nova.DeletedServer](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list deleted servers: %w", err)
	}
	for _, s := range servers {
		if s.ID == serverID {
			return &s, nil
		}
	}
	return nil, nil
}

// GetProjectDomainNames returns a map of project ID to its domain name by joining
// the identity projects and domains. Projects whose domain cannot be resolved are
// omitted from the map.
func (r *NovaReader) GetProjectDomainNames(ctx context.Context) (map[string]string, error) {
	projects, err := listAll[identity.Project](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	domains, err := listAll[identity.Domain](ctx, r.StoreReader)
	if err != nil {
		return nil, fmt.Errorf("failed to list domains: %w", err)
	}
	domainNames := make(map[string]string, len(domains))
	for _, d := range domains {
		domainNames[d.ID] = d.Name
	}
	result := make(map[string]string, len(projects))
	for _, p := range projects {
		if name, ok := domainNames[p.DomainID]; ok {
			result[p.ID] = name
		}
	}
	return result, nil
}

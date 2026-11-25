// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps which projects are pinned to which compute hosts.
type HostPinnedProjects struct {
	// The name of the aggregate where the filter is defined
	AggregateName *string `db:"aggregate_name"`
	// UUID of the aggregate where the filter is defined
	AggregateUUID *string `db:"aggregate_uuid"`
	// Tenant ID that belongs to the filter
	ProjectID *string `db:"project_id"`
	// Domain ID of the project
	DomainID *string `db:"domain_id"`
	// Combination of project name and domain name
	Label *string `db:"label"`
	// Name of the OpenStack compute host.
	ComputeHost *string `db:"compute_host"`
}

// Inputs of the host pinned projects extractor.
type HostPinnedProjectsInputs struct {
	Aggregates  []nova.Aggregate   `input:"aggregates"`
	Hypervisors []nova.Hypervisor  `input:"hypervisors"`
	Projects    []identity.Project `input:"projects"`
	Domains     []identity.Domain  `input:"domains"`
}

// Extractor that extracts the projects pinned to compute hosts.
type HostPinnedProjectsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Metadata key of nova aggregates restricting hosts to specific projects.
const filterTenantIDKey = "filter_tenant_id"

// Extract host-to-project pinning relationships from nova aggregates.
//
// Aggregates with "filter_tenant_id" metadata restrict their hosts to the
// listed (comma-separated) projects. For each listed project, a feature with
// the aggregate, project and a "project_name (domain_name)" label is
// extracted. Hosts of non-ironic hypervisors that are not restricted by any
// aggregate accept any project and are extracted with only the compute host.
func (e *HostPinnedProjectsExtractor) Extract(_ context.Context, in HostPinnedProjectsInputs) ([]HostPinnedProjects, error) {
	projects := make(map[string]identity.Project, len(in.Projects))
	for _, p := range in.Projects {
		projects[p.ID] = p
	}
	domainNames := make(map[string]string, len(in.Domains))
	for _, d := range in.Domains {
		domainNames[d.ID] = d.Name
	}

	restrictedHosts := make(map[string]struct{})
	var features []HostPinnedProjects
	for _, a := range in.Aggregates {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(a.Metadata), &metadata); err != nil {
			log.V(1).Info("skipping aggregate with invalid metadata", "aggregate", a.UUID, "error", err)
			continue
		}
		value, ok := metadata[filterTenantIDKey]
		if !ok {
			continue
		}
		if a.ComputeHost != nil {
			restrictedHosts[*a.ComputeHost] = struct{}{}
		}
		tenantIDs, ok := metadataString(value)
		if !ok || tenantIDs == "" || tenantIDs == "[]" {
			continue
		}
		for tenantID := range strings.SplitSeq(tenantIDs, ",") {
			projectID := strings.TrimSpace(tenantID)
			if projectID == "" {
				continue
			}
			projectName, domainName := "unknown", "unknown"
			var domainID *string
			if p, ok := projects[projectID]; ok {
				projectName = p.Name
				domainID = &p.DomainID
				if name, ok := domainNames[p.DomainID]; ok {
					domainName = name
				}
			}
			label := projectName + " (" + domainName + ")"
			features = append(features, HostPinnedProjects{
				AggregateName: &a.Name,
				AggregateUUID: &a.UUID,
				ProjectID:     &projectID,
				DomainID:      domainID,
				Label:         &label,
				ComputeHost:   a.ComputeHost,
			})
		}
	}
	for _, h := range in.Hypervisors {
		if h.HypervisorType == "ironic" {
			continue
		}
		if _, ok := restrictedHosts[h.ServiceHost]; ok {
			continue
		}
		features = append(features, HostPinnedProjects{ComputeHost: &h.ServiceHost})
	}

	// Sort by compute host and aggregate, unset values last, and drop duplicates.
	slices.SortFunc(features, compareHostPinnedProjects)
	return slices.CompactFunc(features, func(a, b HostPinnedProjects) bool {
		return compareHostPinnedProjects(a, b) == 0
	}), nil
}

// Convert a json metadata value to its text representation, like
// postgres' ->> operator. Returns false for null values.
func metadataString(value any) (string, bool) {
	switch v := value.(type) {
	case nil:
		return "", false
	case string:
		return v, true
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(raw), true
	}
}

// Compare two optional strings, ordering unset values last.
func compareOptional(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return cmp.Compare(*a, *b)
	}
}

func compareHostPinnedProjects(a, b HostPinnedProjects) int {
	return cmp.Or(
		compareOptional(a.ComputeHost, b.ComputeHost),
		compareOptional(a.AggregateUUID, b.AggregateUUID),
		compareOptional(a.ProjectID, b.ProjectID),
		compareOptional(a.AggregateName, b.AggregateName),
		compareOptional(a.DomainID, b.DomainID),
		compareOptional(a.Label, b.Label),
	)
}

func init() {
	extractor.Register("host_pinned_projects_extractor", func() extractor.Extractor[HostPinnedProjectsInputs, HostPinnedProjects] {
		return &HostPinnedProjectsExtractor{}
	})
}

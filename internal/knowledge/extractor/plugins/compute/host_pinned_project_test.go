// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
)

func TestHostPinnedProjectsExtractor_Extract(t *testing.T) {
	projects := []identity.Project{
		{ID: "project_id_1", Name: "project_name_1", DomainID: "domain_id_1"},
		{ID: "project_id_2", Name: "project_name_2", DomainID: "domain_id_2"},
	}
	domains := []identity.Domain{
		{ID: "domain_id_1", Name: "domain_name_1"},
		{ID: "domain_id_2", Name: "domain_name_2"},
	}
	aggregate := func(uuid string, host *string, metadata string) nova.Aggregate {
		return nova.Aggregate{Name: uuid, UUID: uuid, ComputeHost: host, Metadata: metadata}
	}
	hypervisor := func(host, hypervisorType string) nova.Hypervisor {
		return nova.Hypervisor{ID: host, ServiceHost: host, HypervisorType: hypervisorType}
	}
	pinned := func(agg string, host *string, projectID string, domainID *string, label string) HostPinnedProjects {
		return HostPinnedProjects{
			AggregateName: &agg,
			AggregateUUID: &agg,
			ComputeHost:   host,
			ProjectID:     &projectID,
			DomainID:      domainID,
			Label:         &label,
		}
	}
	project1 := func(agg string, host *string) HostPinnedProjects {
		return pinned(agg, host, "project_id_1", new("domain_id_1"), "project_name_1 (domain_name_1)")
	}
	project2 := func(agg string, host *string) HostPinnedProjects {
		return pinned(agg, host, "project_id_2", new("domain_id_2"), "project_name_2 (domain_name_2)")
	}
	unrestricted := func(host string) HostPinnedProjects {
		return HostPinnedProjects{ComputeHost: &host}
	}

	tests := []struct {
		name     string
		in       HostPinnedProjectsInputs
		expected []HostPinnedProjects
	}{
		{
			name: "compute host to project mapping from aggregates",
			in: HostPinnedProjectsInputs{
				Aggregates: []nova.Aggregate{
					aggregate("agg1", new("host2"), `{"filter_tenant_id":"project_id_1, project_id_2"}`),
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_2,project_id_1"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{
				project1("agg1", new("host1")),
				project2("agg1", new("host1")),
				project1("agg1", new("host2")),
				project2("agg1", new("host2")),
			},
		},
		{
			name: "aggregates without filter_tenant_id are ignored",
			in: HostPinnedProjectsInputs{
				Aggregates: []nova.Aggregate{
					aggregate("ignore", new("host1"), `{"something_different":"project_id_1, project_id_2"}`),
				},
			},
			expected: nil,
		},
		{
			name: "aggregates without compute host are sorted last",
			in: HostPinnedProjectsInputs{
				Aggregates: []nova.Aggregate{
					aggregate("agg1", nil, `{"filter_tenant_id":"project_id_1, project_id_2"}`),
					aggregate("agg2", new("host1"), `{"filter_tenant_id":"project_id_1, project_id_2"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{
				project1("agg2", new("host1")),
				project2("agg2", new("host1")),
				project1("agg1", nil),
				project2("agg1", nil),
			},
		},
		{
			name: "empty filter_tenant_id lists are ignored",
			in: HostPinnedProjectsInputs{
				Aggregates: []nova.Aggregate{
					aggregate("agg1", nil, `{"filter_tenant_id":""}`),
					aggregate("agg2", nil, `{"filter_tenant_id":[]}`),
				},
			},
			expected: nil,
		},
		{
			name: "non-ironic hypervisors are unrestricted without aggregates",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{
					hypervisor("host3", "other-not-ironic"),
					hypervisor("host1", "ironic"),
					hypervisor("host2", "not-ironic"),
				},
			},
			expected: []HostPinnedProjects{unrestricted("host2"), unrestricted("host3")},
		},
		{
			name: "hypervisors without filter_tenant_id are unrestricted besides pinned ones",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{
					hypervisor("host1", "not-ironic"),
					hypervisor("host2", "other-not-ironic"),
					hypervisor("host3", "non-filter-host"),
				},
				Aggregates: []nova.Aggregate{
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_1"}`),
					aggregate("agg1", new("host2"), `{"filter_tenant_id":"project_id_1"}`),
					aggregate("az1", new("host1"), `{"type":"az"}`),
					aggregate("az1", new("host2"), `{"type":"az"}`),
					aggregate("az1", new("host3"), `{"type":"az"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{
				project1("agg1", new("host1")),
				project1("agg1", new("host2")),
				unrestricted("host3"),
			},
		},
		{
			name: "duplicate hosts and projects are deduplicated",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{hypervisor("host1", "not-ironic")},
				Aggregates: []nova.Aggregate{
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_1, project_id_1"}`),
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_1, project_id_1"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{project1("agg1", new("host1"))},
		},
		{
			name: "project and host in multiple aggregates",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{hypervisor("host1", "not-ironic")},
				Aggregates: []nova.Aggregate{
					aggregate("agg2", new("host1"), `{"filter_tenant_id":"project_id_1"}`),
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_1"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{project1("agg1", new("host1")), project1("agg2", new("host1"))},
		},
		{
			name: "unknown label if project or domain is missing",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{hypervisor("host1", "not-ironic")},
				Aggregates: []nova.Aggregate{
					aggregate("agg1", new("host1"), `{"filter_tenant_id":"project_id_unknown, project_id_domain_unknown, project_id_1"}`),
				},
				Projects: append(projects, identity.Project{ID: "project_id_domain_unknown", Name: "project_name_3", DomainID: "domain_id_unknown"}),
				Domains:  domains,
			},
			expected: []HostPinnedProjects{
				project1("agg1", new("host1")),
				pinned("agg1", new("host1"), "project_id_domain_unknown", new("domain_id_unknown"), "project_name_3 (unknown)"),
				pinned("agg1", new("host1"), "project_id_unknown", nil, "unknown (unknown)"),
			},
		},
		{
			name: "aggregates with invalid metadata are skipped",
			in: HostPinnedProjectsInputs{
				Hypervisors: []nova.Hypervisor{hypervisor("host1", "not-ironic"), hypervisor("host2", "not-ironic")},
				Aggregates: []nova.Aggregate{
					aggregate("agg1", new("host1"), `not json`),
					aggregate("agg2", new("host2"), `{"filter_tenant_id":"project_id_2"}`),
				},
				Projects: projects,
				Domains:  domains,
			},
			expected: []HostPinnedProjects{unrestricted("host1"), project2("agg2", new("host2"))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, err := (&HostPinnedProjectsExtractor{}).Extract(t.Context(), tt.in)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !reflect.DeepEqual(features, tt.expected) {
				t.Errorf("expected:\n%s\ngot:\n%s", formatHostPinnedProjects(tt.expected), formatHostPinnedProjects(features))
			}
		})
	}
}

func formatHostPinnedProjects(features []HostPinnedProjects) string {
	deref := func(s *string) string {
		if s == nil {
			return "<nil>"
		}
		return *s
	}
	lines := make([]string, 0, len(features))
	for _, f := range features {
		lines = append(lines, fmt.Sprintf("{AggUUID: %s, AggName: %s, Host: %s, ProjectID: %s, DomainID: %s, Label: %s}",
			deref(f.AggregateUUID), deref(f.AggregateName), deref(f.ComputeHost), deref(f.ProjectID), deref(f.DomainID), deref(f.Label)))
	}
	return strings.Join(lines, "\n")
}

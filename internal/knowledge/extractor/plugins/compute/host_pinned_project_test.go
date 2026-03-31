// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

func TestHostPinnedProjectsExtractor_Init(t *testing.T) {
	extractor := &HostPinnedProjectsExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostPinnedProjectsExtractor_Extract(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	tests := []struct {
		name     string
		mockData []any
		expected []HostPinnedProjects
	}{
		{
			name: "find compute host to project mapping from aggregates",
			mockData: []any{
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
				},
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host2"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
				},

				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},
				&identity.Project{
					ID:       "project_id_2",
					Name:     "project_name_2",
					DomainID: "domain_id_2",
				},
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
				&identity.Domain{
					ID:   "domain_id_2",
					Name: "domain_name_2",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_2"),
					DomainID:      testlib.Ptr("domain_id_2"),
					Label:         testlib.Ptr("project_name_2 (domain_name_2)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_2"),
					DomainID:      testlib.Ptr("domain_id_2"),
					Label:         testlib.Ptr("project_name_2 (domain_name_2)"),
				},
			},
		},
		{
			name: "ignore aggregates without filter_tenant_id",
			mockData: []any{
				&nova.Aggregate{
					Name:        "ignore-no-filter-tenant",
					UUID:        "ignore",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"something_different":"project_id_1, project_id_2"}`,
				},
			},
			expected: []HostPinnedProjects{},
		},
		{
			name: "support filter_tenant_id with no compute host assigned to the aggregate",
			mockData: []any{
				// This aggregate doesn't have a compute host so project_3 and 4 should have an empty entry for the compute host
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: nil,
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
				},
				// Because of this aggregate project 3 and 4 should additionally have a host-4 as pinned
				&nova.Aggregate{
					Name:        "agg2",
					UUID:        "agg2",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
				},

				// Projects
				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},
				&identity.Project{
					ID:       "project_id_2",
					Name:     "project_name_2",
					DomainID: "domain_id_2",
				},
				// Domains
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
				&identity.Domain{
					ID:   "domain_id_2",
					Name: "domain_name_2",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_2"),
					DomainID:      testlib.Ptr("domain_id_2"),
					Label:         testlib.Ptr("project_name_2 (domain_name_2)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   nil,
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   nil,
					ProjectID:     testlib.Ptr("project_id_2"),
					DomainID:      testlib.Ptr("domain_id_2"),
					Label:         testlib.Ptr("project_name_2 (domain_name_2)"),
				},
			},
		},
		{
			name: "filter out empty filter_tenant_id lists",
			mockData: []any{
				// Doesn't have any filter_tenant_id set, so this aggregate is supposed to be ignored
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: nil,
					Metadata:    `{"filter_tenant_id":""}`,
				},
				&nova.Aggregate{
					Name:        "agg2",
					UUID:        "agg2",
					ComputeHost: nil,
					Metadata:    `{"filter_tenant_id":[]}`,
				},
			},
			expected: []HostPinnedProjects{},
		},
		{
			name: "find all hypervisors if no aggregate is provided",
			mockData: []any{
				// Doesn't have any filter_tenant_id set, so this aggregate is supposed to be ignored
				&nova.Hypervisor{
					ID:             "1",
					ServiceHost:    "host1",
					HypervisorType: "ironic",
				},
				&nova.Hypervisor{
					ID:             "2",
					ServiceHost:    "host2",
					HypervisorType: "not-ironic",
				},
				// Ignore ironic hypervisors
				&nova.Hypervisor{
					ID:             "3",
					ServiceHost:    "host3",
					HypervisorType: "other-not-ironic",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     nil,
					DomainID:      nil,
					Label:         nil,
				},
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host3"),
					ProjectID:     nil,
					DomainID:      nil,
					Label:         nil,
				},
			},
		},
		{
			name: "check if all hypervisors without filter_tenant_id are found when aggregates with filter_tenant_id exist",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{
					ID:             "1",
					ServiceHost:    "host1",
					HypervisorType: "not-ironic",
				},
				&nova.Hypervisor{
					ID:             "2",
					ServiceHost:    "host2",
					HypervisorType: "other-not-ironic",
				},
				&nova.Hypervisor{
					ID:             "3",
					ServiceHost:    "host3",
					HypervisorType: "non-filter-host",
				},

				// Aggregates
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1"}`,
				},
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host2"),
					Metadata:    `{"filter_tenant_id":"project_id_1"}`,
				},
				&nova.Aggregate{
					Name:        "az1",
					UUID:        "az1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"type":"az"}`,
				},
				&nova.Aggregate{
					Name:        "az1",
					UUID:        "az1",
					ComputeHost: testlib.Ptr("host2"),
					Metadata:    `{"type":"az"}`,
				},
				// Host 3 is part of an availability zone aggregate, but has no filter_tenant_id
				&nova.Aggregate{
					Name:        "az1",
					UUID:        "az1",
					ComputeHost: testlib.Ptr("host3"),
					Metadata:    `{"type":"az"}`,
				},

				// Projects
				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},
				// Domains
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host3"),
					ProjectID:     nil,
					DomainID:      nil,
					Label:         nil,
				},
			},
		},
		{
			name: "check behavior with duplicate hosts and projects in one aggregate",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{
					ID:             "1",
					ServiceHost:    "host1",
					HypervisorType: "not-ironic",
				},

				// Aggregates
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_1"}`,
				},
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_1"}`,
				},

				// Projects
				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},

				// Domains
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
			},
		},
		{
			name: "check behavior if project id and host are part of multiple aggregates",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{
					ID:             "1",
					ServiceHost:    "host1",
					HypervisorType: "not-ironic",
				},

				// Aggregates
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1"}`,
				},
				&nova.Aggregate{
					Name:        "agg2",
					UUID:        "agg2",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1"}`,
				},

				// Projects
				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},
				// Domains
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
			},
		},
		{
			name: "Expect unknown label if project or domain is missing",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{
					ID:             "1",
					ServiceHost:    "host1",
					HypervisorType: "not-ironic",
				},

				// Aggregates
				&nova.Aggregate{
					Name:        "agg1",
					UUID:        "agg1",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_1, project_id_domain_unknown, project_id_unknown"}`,
				},

				// Projects
				&identity.Project{
					ID:       "project_id_1",
					Name:     "project_name_1",
					DomainID: "domain_id_1",
				},
				&identity.Project{
					ID:       "project_id_domain_unknown",
					Name:     "project_name_2",
					DomainID: "domain_id_unknown",
				},
				// Domains
				&identity.Domain{
					ID:   "domain_id_1",
					Name: "domain_name_1",
				},
			},
			expected: []HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
					DomainID:      testlib.Ptr("domain_id_1"),
					Label:         testlib.Ptr("project_name_1 (domain_name_1)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_domain_unknown"),
					DomainID:      testlib.Ptr("domain_id_unknown"),
					Label:         testlib.Ptr("project_name_2 (unknown)"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_unknown"),
					DomainID:      nil,
					Label:         testlib.Ptr("unknown (unknown)"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(nova.Aggregate{}),
				testDB.AddTable(nova.Hypervisor{}),
				testDB.AddTable(identity.Project{}),
				testDB.AddTable(identity.Domain{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			extractor := &HostPinnedProjectsExtractor{}
			config := v1alpha1.KnowledgeSpec{}

			if err := extractor.Init(&testDB, nil, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			features, err := extractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check if the expected hosts match the extracted ones
			if len(features) != len(tt.expected) {
				t.Fatalf("expected %d host pinned projects, got %d", len(tt.expected), len(features))
			}

			for _, f := range features {
				hpp := f.(HostPinnedProjects)
				found := false
				for _, exp := range tt.expected {
					if reflect.DeepEqual(hpp, exp) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("unexpected host pinned project found: %s", printHostPinnedProject(hpp))
				}
			}
		})
	}
}

func printHostPinnedProject(hpp HostPinnedProjects) string {
	var aggUUID, aggName, computeHost, projectID, domainID, label string

	if hpp.AggregateUUID != nil {
		aggUUID = *hpp.AggregateUUID
	} else {
		aggUUID = "<nil>"
	}

	if hpp.AggregateName != nil {
		aggName = *hpp.AggregateName
	} else {
		aggName = "<nil>"
	}

	if hpp.ComputeHost != nil {
		computeHost = *hpp.ComputeHost
	} else {
		computeHost = "<nil>"
	}

	if hpp.ProjectID != nil {
		projectID = *hpp.ProjectID
	} else {
		projectID = "<nil>"
	}

	if hpp.DomainID != nil {
		domainID = *hpp.DomainID
	} else {
		domainID = "<nil>"
	}

	if hpp.Label != nil {
		label = *hpp.Label
	} else {
		label = "<nil>"
	}

	return fmt.Sprintf("{AggUUID: %s, AggName: %s, Host: %s, ProjectID: %s, DomainID: %s, Label: %s}",
		aggUUID, aggName, computeHost, projectID, domainID, label)
}

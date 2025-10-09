// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/conf"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostPinnedProjectsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(shared.HostPinnedProjects{}) {
		t.Error("expected table to be created")
	}
}

func TestHostPinnedProjectsExtractor_Extract(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	tests := []struct {
		name     string
		mockData []any
		expected []shared.HostPinnedProjects
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
			},
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_2"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_2"),
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
			expected: []shared.HostPinnedProjects{},
		},
		{
			name: "support filter_tenant_id with no compute host assigned to the aggregate",
			mockData: []any{
				// This aggregate doesn't have a compute host so project_3 and 4 should have an empty entry for the compute host
				&nova.Aggregate{
					Name:        "agg2",
					UUID:        "agg2",
					ComputeHost: nil,
					Metadata:    `{"filter_tenant_id":"project_id_3, project_id_4"}`,
				},
				// Because of this aggregate project 3 and 4 should additionally have a host-4 as pinned
				&nova.Aggregate{
					Name:        "agg3",
					UUID:        "agg3",
					ComputeHost: testlib.Ptr("host1"),
					Metadata:    `{"filter_tenant_id":"project_id_3, project_id_4"}`,
				},
			},
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg3"),
					AggregateUUID: testlib.Ptr("agg3"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_3"),
				},
				{
					AggregateName: testlib.Ptr("agg3"),
					AggregateUUID: testlib.Ptr("agg3"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_4"),
				},
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   nil,
					ProjectID:     testlib.Ptr("project_id_3"),
				},
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   nil,
					ProjectID:     testlib.Ptr("project_id_4"),
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
			expected: []shared.HostPinnedProjects{},
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
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     nil,
				},
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host3"),
					ProjectID:     nil,
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
			},
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host2"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
				{
					AggregateName: nil,
					AggregateUUID: nil,
					ComputeHost:   testlib.Ptr("host3"),
					ProjectID:     nil,
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
			},
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
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
			},
			expected: []shared.HostPinnedProjects{
				{
					AggregateName: testlib.Ptr("agg1"),
					AggregateUUID: testlib.Ptr("agg1"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
				{
					AggregateName: testlib.Ptr("agg2"),
					AggregateUUID: testlib.Ptr("agg2"),
					ComputeHost:   testlib.Ptr("host1"),
					ProjectID:     testlib.Ptr("project_id_1"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer testDB.Close()
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(nova.Aggregate{}),
				testDB.AddTable(nova.Hypervisor{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			extractor := &HostPinnedProjectsExtractor{}
			config := conf.FeatureExtractorConfig{
				Name:           "host_pinned_projects_extractor",
				Options:        libconf.NewRawOpts("{}"),
				RecencySeconds: nil,
			}

			if err := extractor.Init(testDB, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if _, err := extractor.Extract(); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			var hostPinnedProjects []shared.HostPinnedProjects
			table := shared.HostPinnedProjects{}.TableName()
			if _, err := testDB.Select(&hostPinnedProjects, "SELECT * FROM "+table); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check if the expected hosts match the extracted ones
			if len(hostPinnedProjects) != len(tt.expected) {
				t.Fatalf("expected %d host pinned projects, got %d", len(tt.expected), len(hostPinnedProjects))
			}
			// Compare each expected host with the extracted ones
			if !reflect.DeepEqual(tt.expected, hostPinnedProjects) {
				t.Errorf("expected %v, got %v", tt.expected, hostPinnedProjects)
			}
		})
	}
}

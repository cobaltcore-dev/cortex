// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestFilterProjectAggregatesStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostPinnedProjects{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostPinnedProjects := []any{
		// Host1 has no assigned filter_tenant_id - should always be included
		&shared.HostPinnedProjects{
			AggregateName: nil,
			AggregateUUID: nil,
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     nil,
		},
		// Aggregate 2 maps to project-123 to host2
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg2"),
			AggregateUUID: testlib.Ptr("aggregate2"),
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     testlib.Ptr("project-123"),
		},
		// Aggregate 3 maps to project-456 to host3
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg3"),
			AggregateUUID: testlib.Ptr("aggregate3"),
			ComputeHost:   testlib.Ptr("host3"),
			ProjectID:     testlib.Ptr("project-456"),
		},
		// Aggregate 4 maps to project-123 and project-789 to host4
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg4"),
			AggregateUUID: testlib.Ptr("agg4"),
			ComputeHost:   testlib.Ptr("host4"),
			ProjectID:     testlib.Ptr("project-123"),
		},
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg4"),
			AggregateUUID: testlib.Ptr("agg4"),
			ComputeHost:   testlib.Ptr("host4"),
			ProjectID:     testlib.Ptr("project-789"),
		},
		// Host5 has no assigned filter_tenant_id - should always be included
		&shared.HostPinnedProjects{
			AggregateName: nil,
			AggregateUUID: nil,
			ComputeHost:   testlib.Ptr("host5"),
			ProjectID:     nil,
		},
		// Aggregate 6 has no hosts assigned but a tenant filter
		// This should not have any effect on the filter
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg6"),
			AggregateUUID: testlib.Ptr("aggregate6"),
			ComputeHost:   nil,
			ProjectID:     testlib.Ptr("project-123"),
		},
		// Maps project-123 to host2 a second time to test DISTINCT in SQL
		&shared.HostPinnedProjects{
			AggregateName: testlib.Ptr("agg7"),
			AggregateUUID: testlib.Ptr("aggregate7"),
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     testlib.Ptr("project-123"),
		},
	}

	if err := testDB.Insert(hostPinnedProjects...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.PipelineRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No project ID - no filtering",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Project matches aggregate filter",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4", "host5"}, // host1 (no filter), host2 (matches), host4 (matches), host5 (no filter)
			filteredHosts: []string{"host3"},                            // host3 has filter for different project
		},
		{
			name: "Project matches different aggregate filter",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-456",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host5"}, // host1 (no filter), host3 (matches), host5 (no filter)
			filteredHosts: []string{"host2", "host4"},          // host2 and host4 have filters for different projects
		},
		{
			name: "Project matches multiple project filter",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-789",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host4", "host5"}, // host1 (no filter), host4 (matches), host5 (no filter)
			filteredHosts: []string{"host2", "host3"},          // host2 and host3 have filters for different projects
		},
		{
			name: "Project doesn't match any filter",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-nonexistent",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"}, // Only hosts without tenant filters
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Only hosts without filters",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Only hosts with matching filters",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Only hosts with non-matching filters",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Host not in database",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
		{
			name: "Empty host list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterProjectAggregatesStep{}
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}

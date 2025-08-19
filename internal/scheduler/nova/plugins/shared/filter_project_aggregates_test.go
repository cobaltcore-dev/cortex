// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
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
		testDB.AddTable(nova.Aggregate{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock aggregate data
	aggregates := []any{
		&nova.Aggregate{UUID: "agg1", Name: "aggregate1", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: testlib.Ptr("host1"), Metadata: "{}"},
		&nova.Aggregate{UUID: "agg2", Name: "aggregate2", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: testlib.Ptr("host2"), Metadata: `{"filter_tenant_id": "project-123"}`},
		&nova.Aggregate{UUID: "agg3", Name: "aggregate3", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: testlib.Ptr("host3"), Metadata: `{"filter_tenant_id": "project-456"}`},
		&nova.Aggregate{UUID: "agg4", Name: "aggregate4", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: testlib.Ptr("host4"), Metadata: `{"filter_tenant_id": "project-123,project-789"}`},
		&nova.Aggregate{UUID: "agg5", Name: "aggregate5", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: testlib.Ptr("host5"), Metadata: `{"other_metadata": "value"}`},
		&nova.Aggregate{UUID: "agg6", Name: "aggregate6", AvailabilityZone: testlib.Ptr("az-1"), ComputeHost: nil, Metadata: `{"filter_tenant_id": "project-123"}`},
	}
	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No project ID - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-456",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-789",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-nonexistent",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
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
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Only hosts with matching filters",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Only hosts with non-matching filters",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-123",
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
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

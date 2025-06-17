// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestAvoidOverloadedHostsMemoryStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(kvm.NodeExporterHostMemoryActive{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_memory_active table
	_, err = testDB.Exec(`
        INSERT INTO feature_host_memory_active
			(compute_host, avg_memory_active, max_memory_active)
        VALUES
            ('host1', 15.0, 25.0),
            ('host2', 5.0, 10.0),
            ('host3', 20.0, 30.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "avgMemoryUsageLowerBound": 10,
        "avgMemoryUsageUpperBound": 100,
        "avgMemoryUsageActivationLowerBound": 0.0,
        "avgMemoryUsageActivationUpperBound": -0.5,
        "maxMemoryUsageLowerBound": 20,
        "maxMemoryUsageUpperBound": 100,
        "maxMemoryUsageActivationLowerBound": 0.0,
        "maxMemoryUsageActivationUpperBound": -0.5
    }`)
	step := &AvoidOverloadedHostsMemoryStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Non-vmware vm",
			request: api.ExternalSchedulerRequest{
				VMware: false,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			// Should downvote hosts with high CPU usage
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host3": {},
			},
		},
		{
			name: "No overloaded hosts",
			request: api.ExternalSchedulerRequest{
				VMware: false,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			// Should not downvote any hosts
			downvotedHosts: map[string]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for host, weight := range result.Activations {
				if _, ok := tt.downvotedHosts[host]; ok {
					if weight >= 0 {
						t.Errorf("expected weight for host %s to be less than 0, got %f", host, weight)
					}
				} else {
					if weight != 0 {
						t.Errorf("expected weight for host %s to be 0, got %f", host, weight)
					}
				}
			}
		})
	}
}

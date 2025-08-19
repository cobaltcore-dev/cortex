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

func TestAvoidOverloadedHostsCPUStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(kvm.NodeExporterHostCPUUsage{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostCPUUsage := []any{
		&kvm.NodeExporterHostCPUUsage{ComputeHost: "host1", AvgCPUUsage: 15.0, MaxCPUUsage: 25.0},
		&kvm.NodeExporterHostCPUUsage{ComputeHost: "host2", AvgCPUUsage: 5.0, MaxCPUUsage: 10.0},
		&kvm.NodeExporterHostCPUUsage{ComputeHost: "host3", AvgCPUUsage: 20.0, MaxCPUUsage: 30.0},
	}
	if err := testDB.Insert(hostCPUUsage...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "avgCPUUsageLowerBound": 10,
        "avgCPUUsageUpperBound": 100,
        "avgCPUUsageActivationLowerBound": 0.0,
        "avgCPUUsageActivationUpperBound": -0.5,
        "maxCPUUsageLowerBound": 20,
        "maxCPUUsageUpperBound": 100,
        "maxCPUUsageActivationLowerBound": 0.0,
        "maxCPUUsageActivationUpperBound": -0.5
    }`)
	step := &AvoidOverloadedHostsCPUStep{}
	if err := step.Init("", testDB, opts); err != nil {
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

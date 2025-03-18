// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
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

	// Insert mock data into the feature_host_cpu_usage table
	_, err = testDB.Exec(`
        INSERT INTO feature_host_cpu_usage (compute_host, avg_cpu_usage, max_cpu_usage)
        VALUES
            ('host1', 15.0, 25.0),
            ('host2', 5.0, 10.0),
            ('host3', 20.0, 30.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`
        # Min-max scaling for avg CPU usage on the host.
        avgCPUUsageLowerBound: 10
        avgCPUUsageUpperBound: 100
        avgCPUUsageActivationLowerBound: 0.0
        avgCPUUsageActivationUpperBound: -0.5
        # Min-max scaling for max CPU usage on the host.
        maxCPUUsageLowerBound: 20
        maxCPUUsageUpperBound: 100
        maxCPUUsageActivationLowerBound: 0.0
        maxCPUUsageActivationUpperBound: -0.5
    `)
	step := &AvoidOverloadedHostsCPUStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name           string
		request        api.Request
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Non-vmware vm",
			request: api.Request{
				VMware: false,
				Hosts: []api.Host{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			// Should downvote hosts with high CPU usage
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host3": {},
			},
		},
		{
			name: "VMware vm",
			request: api.Request{
				VMware: true,
				Hosts: []api.Host{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			// Should not do anything for VMware VMs
			downvotedHosts: map[string]struct{}{},
		},
		{
			name: "No overloaded hosts",
			request: api.Request{
				VMware: false,
				Hosts: []api.Host{
					{ComputeHost: "host4", HypervisorHostname: "hypervisor4"},
					{ComputeHost: "host5", HypervisorHostname: "hypervisor5"},
				},
			},
			// Should not downvote any hosts
			downvotedHosts: map[string]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights, err := step.Run(tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for host, weight := range weights {
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

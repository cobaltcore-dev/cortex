// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	testlibPlugins "github.com/cobaltcore-dev/cortex/testlib/scheduler/plugins"
)

func TestAvoidContendedHostsStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(vmware.VROpsHostsystemContention{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_vrops_hostsystem_contention table
	_, err = testDB.Exec(`
        INSERT INTO feature_vrops_hostsystem_contention (compute_host, avg_cpu_contention, max_cpu_contention)
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
        # Min-max scaling for avg CPU contention on the host.
        avgCPUContentionLowerBound: 10
        avgCPUContentionUpperBound: 100
        avgCPUContentionActivationLowerBound: 0.0
        avgCPUContentionActivationUpperBound: -0.5
        # Min-max scaling for max CPU contention on the host.
        maxCPUContentionLowerBound: 20
        maxCPUContentionUpperBound: 100
        maxCPUContentionActivationLowerBound: 0.0
        maxCPUContentionActivationUpperBound: -0.5
    `)
	step := &AvoidContendedHostsStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name           string
		scenario       plugins.Scenario
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Non-vmware vm",
			scenario: &testlibPlugins.MockScenario{
				VMware: false,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			// Should not do anything
			downvotedHosts: map[string]struct{}{},
		},
		{
			name: "Avoid contended hosts",
			scenario: &testlibPlugins.MockScenario{
				VMware: true,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host3": {},
			},
		},
		{
			name: "No contended hosts",
			scenario: &testlibPlugins.MockScenario{
				VMware: true,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host4", HypervisorHostname: "hypervisor4"},
					{ComputeHost: "host5", HypervisorHostname: "hypervisor5"},
				},
			},
			downvotedHosts: map[string]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights, err := step.Run(tt.scenario)
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

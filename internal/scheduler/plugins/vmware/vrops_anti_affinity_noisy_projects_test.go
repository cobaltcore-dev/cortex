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

func TestAntiAffinityNoisyProjectsStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(vmware.VROpsProjectNoisiness{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_vrops_project_noisiness table
	_, err = testDB.Exec(`
        INSERT INTO feature_vrops_project_noisiness (project, compute_host, avg_cpu_of_project)
        VALUES
            ('project1', 'host1', 25.0),
            ('project1', 'host2', 30.0),
            ('project2', 'host3', 15.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	opts := conf.NewRawOpts(`
        avgCPUThreshold: 20.0
        activationOnHit: -1.0
    `)
	step := &VROpsAntiAffinityNoisyProjectsStep{}
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
				ProjectID: "project1",
				VMware:    false,
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
			name: "Noisy project",
			scenario: &testlibPlugins.MockScenario{
				ProjectID: "project1",
				VMware:    true,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host2": {},
			},
		},
		{
			name: "Non-noisy project",
			scenario: &testlibPlugins.MockScenario{
				ProjectID: "project2",
				VMware:    true,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			downvotedHosts: map[string]struct{}{},
		},
		{
			name: "No noisy project data",
			scenario: &testlibPlugins.MockScenario{
				ProjectID: "project3",
				VMware:    true,
				Hosts: []testlibPlugins.MockScenarioHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
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

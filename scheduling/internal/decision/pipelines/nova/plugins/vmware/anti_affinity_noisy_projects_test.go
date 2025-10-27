// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
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

	vropsProjectNoisiness := []any{
		&vmware.VROpsProjectNoisiness{Project: "project1", ComputeHost: "host1", AvgCPUOfProject: 25.0},
		&vmware.VROpsProjectNoisiness{Project: "project1", ComputeHost: "host2", AvgCPUOfProject: 30.0},
		&vmware.VROpsProjectNoisiness{Project: "project2", ComputeHost: "host3", AvgCPUOfProject: 15.0},
	}
	if err := testDB.Insert(vropsProjectNoisiness...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	opts := conf.NewRawOpts(`{
        "avgCPUUsageLowerBound": 20,
        "avgCPUUsageUpperBound": 100,
        "avgCPUUsageActivationLowerBound": 0.0,
        "avgCPUUsageActivationUpperBound": -0.5
    }`)
	step := &AntiAffinityNoisyProjectsStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Noisy project",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project1",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host2": {},
			},
		},
		{
			name: "Non-noisy project",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project2",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			downvotedHosts: map[string]struct{}{},
		},
		{
			name: "No noisy project data",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project3",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
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

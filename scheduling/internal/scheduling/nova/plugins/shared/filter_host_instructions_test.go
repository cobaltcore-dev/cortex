// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	delegationAPI "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/nova/api"
)

func TestFilterHostInstructionsStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       api.PipelineRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No host instructions - no filtering",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Ignore single host",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host2"},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Ignore multiple hosts",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host1", "host3", "host4"},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Ignore all hosts",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host1", "host2", "host3", "host4"},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4"},
		},
		{
			name: "Ignore non-existent host",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host-nonexistent"},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Force single host",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  &[]string{"host2"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Force multiple hosts",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  &[]string{"host1", "host3"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Force non-existent host",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  &[]string{"host-nonexistent"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "Force and ignore same host - ignore takes precedence",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host2"},
						ForceHosts:  &[]string{"host2"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "Force and ignore different hosts",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host1"},
						ForceHosts:  &[]string{"host2", "host3"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "Complex scenario - multiple ignore and force",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host2", "host5"},
						ForceHosts:  &[]string{"host1", "host3", "host4"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{"host2", "host5", "host6"},
		},
		{
			name: "Empty host list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host1"},
						ForceHosts:  &[]string{"host2"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Empty ignore list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Empty force list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  &[]string{},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Both lists empty",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{},
						ForceHosts:  &[]string{},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Duplicate hosts in ignore list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: &[]string{"host1", "host1", "host2"},
						ForceHosts:  nil,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Duplicate hosts in force list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						IgnoreHosts: nil,
						ForceHosts:  &[]string{"host1", "host1", "host2"},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterHostInstructionsStep{}
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

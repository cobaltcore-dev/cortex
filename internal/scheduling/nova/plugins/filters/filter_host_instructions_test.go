// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

func TestFilterHostInstructionsStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       compute.PlacementRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No host instructions - no filtering",
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host2"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host1", "host3", "host4"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host1", "host2", "host3", "host4"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host-nonexistent"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				ForceHosts: []string{"host2"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				ForceHosts: []string{"host1", "host3"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				ForceHosts: []string{"host-nonexistent"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host2"},
				ForceHosts:  []string{"host2"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host1"},
				ForceHosts:  []string{"host2", "host3"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host2", "host5"},
				ForceHosts:  []string{"host1", "host3", "host4"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host1"},
				ForceHosts:  []string{"host2"},
				Hosts:       []compute.Host{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Empty ignore list",
			request: compute.PlacementRequest{
				IgnoreHosts: []string{},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Empty force list",
			request: compute.PlacementRequest{
				ForceHosts: []string{},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			// An empty force list is no constraint in the typed spec (it cannot be
			// distinguished from "unset"), so all hosts are kept.
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Both lists empty",
			request: compute.PlacementRequest{
				IgnoreHosts: []string{},
				ForceHosts:  []string{},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Duplicate hosts in ignore list",
			request: compute.PlacementRequest{
				IgnoreHosts: []string{"host1", "host1", "host2"},
				Hosts: []compute.Host{
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
			request: compute.PlacementRequest{
				ForceHosts: []string{"host1", "host1", "host2"},
				Hosts: []compute.Host{
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
			result, err := step.Run(logr.Discard(), tt.request)
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

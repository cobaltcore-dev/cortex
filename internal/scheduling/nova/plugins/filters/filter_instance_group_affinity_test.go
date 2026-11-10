// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

func TestFilterInstanceGroupAffinityStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       compute.PlacementRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No instance group - all hosts pass",
			request: compute.PlacementRequest{
				InstanceGroup: nil,
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
			name: "Instance group with anti-affinity policy - all hosts pass",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "anti-affinity", Hosts: []string{"host1", "host2"}},
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
			name: "Instance group with soft-affinity policy - all hosts pass",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "soft-affinity", Hosts: []string{"host1"}},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Affinity policy with empty hosts list - all hosts pass",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{}},
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
			name: "Affinity policy - only hosts in instance group pass",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1", "host3"}},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Affinity policy - single host in group",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host2"}},
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
			name: "Affinity policy - all hosts filtered out",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host5", "host6"}},
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
			name: "Affinity policy - all hosts in group pass",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1", "host2", "host3"}},
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
			name: "Affinity policy - partial overlap",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host2", "host3", "host5"}},
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
			name: "Affinity policy - empty request hosts",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1", "host2"}},
				Hosts:         []compute.Host{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Affinity policy - case sensitive host matching",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"Host1", "HOST2"}},
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
			name: "Affinity policy - with instance UUID and project ID",
			request: compute.PlacementRequest{
				InstanceUUID:  "vm-uuid-123",
				ProjectID:     "project-abc",
				InstanceGroup: &api.InstanceGroupSpec{UUID: "ig-uuid-456", Policy: "affinity", Hosts: []string{"host1", "host2"}, Members: []string{"vm-uuid-789"}},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Affinity policy - duplicate hosts in group list",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1", "host2", "host1", "host2"}},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Affinity policy - single host scenario",
			request: compute.PlacementRequest{
				InstanceGroup: &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1"}},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterInstanceGroupAffinityStep{}
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

func TestFilterInstanceGroupAffinityStep_SkipsForNonPlacementIntent(t *testing.T) {
	// Affinity group on host1 only — host2 would normally be filtered.
	for _, intent := range []string{"reserve_for_failover", "reuse_failover_reservation", "reserve_for_committed_resource", "capacity_probe"} {
		t.Run(intent, func(t *testing.T) {
			request := newNovaRequest("vm", "proj", "gp", 1, "1Gi", false, []string{"host1", "host2"})
			request.InstanceGroup = &api.InstanceGroupSpec{Policy: "affinity", Hosts: []string{"host1"}}
			request.Intent = v1alpha1.SchedulingIntent(intent)
			step := &FilterInstanceGroupAffinityStep{}

			result, err := step.Run(logr.Discard(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Activations) != 2 {
				t.Errorf("expected both hosts to pass, got %d", len(result.Activations))
			}
		})
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"testing"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

func TestFilterDomainPinningStep_Run(t *testing.T) {
	opts := FilterDomainPinningStepOpts{
		DomainHypervisors: map[string][]string{
			"ext-pinned-a": {"host1", "host2"},
			"ext-pinned-b": {"host3"},
		},
	}
	allHosts := []compute.Host{
		{ComputeHost: "host1"},
		{ComputeHost: "host2"},
		{ComputeHost: "host3"},
		{ComputeHost: "host4"},
	}

	tests := []struct {
		name          string
		request       compute.PlacementRequest
		expectedHosts []string
	}{
		{
			name:          "Pinned domain keeps only its hypervisors",
			request:       compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: allHosts},
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:          "Pinned domain with single hypervisor",
			request:       compute.PlacementRequest{DomainName: "ext-pinned-b", Hosts: allHosts},
			expectedHosts: []string{"host3"},
		},
		{
			name:          "Unpinned domain is kept off all reserved hypervisors",
			request:       compute.PlacementRequest{DomainName: "ext-other", Hosts: allHosts},
			expectedHosts: []string{"host4"},
		},
		{
			// Request offers only a subset of the pinned domain's hosts (host2 of
			// {host1, host2}); the filter keeps just that one, never re-adding host1.
			name: "Pinned domain keeps only the offered subset of its hypervisors",
			request: compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: []compute.Host{
				{ComputeHost: "host2"},
				{ComputeHost: "host4"},
			}},
			expectedHosts: []string{"host2"},
		},
		{
			// Request offers none of the pinned domain's hosts → placement fails.
			name: "Pinned domain gets no host when none of its hypervisors are offered",
			request: compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: []compute.Host{
				{ComputeHost: "host3"},
				{ComputeHost: "host4"},
			}},
			expectedHosts: []string{},
		},
		{
			name:          "Failover reservation for pinned domain is confined to its hypervisors",
			request:       compute.PlacementRequest{Intent: api.ReserveForFailoverIntent, DomainName: "ext-pinned-a", Hosts: allHosts},
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:          "Capacity probe (no domain) excludes all pinned hypervisors",
			request:       compute.PlacementRequest{Intent: api.CapacityProbeIntent, Hosts: allHosts},
			expectedHosts: []string{"host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterDomainPinningStep{}
			step.Options = opts
			result, err := step.Run(logr.Discard(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Fatalf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
		})
	}
}

// TestFilterDomainPinningStep_OverlappingHypervisors covers the case where the
// same hypervisor is pinned to more than one domain. Such a shared host must be
// usable by every domain that pins it, and by no other domain.
func TestFilterDomainPinningStep_OverlappingHypervisors(t *testing.T) {
	// host-shared is pinned to both ext-pinned-a and ext-pinned-b.
	opts := FilterDomainPinningStepOpts{
		DomainHypervisors: map[string][]string{
			"ext-pinned-a": {"host-a", "host-shared"},
			"ext-pinned-b": {"host-b", "host-shared"},
		},
	}
	allHosts := []compute.Host{
		{ComputeHost: "host-a"},
		{ComputeHost: "host-b"},
		{ComputeHost: "host-shared"},
		{ComputeHost: "host-free"},
	}

	tests := []struct {
		name          string
		request       compute.PlacementRequest
		expectedHosts []string
	}{
		{
			name:          "First owner keeps its own and the shared hypervisor",
			request:       compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: allHosts},
			expectedHosts: []string{"host-a", "host-shared"},
		},
		{
			name:          "Second owner keeps its own and the shared hypervisor",
			request:       compute.PlacementRequest{DomainName: "ext-pinned-b", Hosts: allHosts},
			expectedHosts: []string{"host-b", "host-shared"},
		},
		{
			name:          "Unpinned domain is kept off every pinned hypervisor including the shared one",
			request:       compute.PlacementRequest{DomainName: "ext-other", Hosts: allHosts},
			expectedHosts: []string{"host-free"},
		},
		{
			// The request offers only a subset of the domain's pinned hosts. The
			// filter must keep just the intersection and never re-add the missing
			// pinned host (host-a).
			name: "Owner keeps only the offered subset of its pinned hypervisors",
			request: compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: []compute.Host{
				{ComputeHost: "host-shared"},
				{ComputeHost: "host-free"},
			}},
			expectedHosts: []string{"host-shared"},
		},
		{
			// The request does not offer any of the domain's pinned hosts, so no
			// host survives (placement fails) rather than re-adding a pinned host.
			name: "Owner gets no host when none of its pinned hosts are offered",
			request: compute.PlacementRequest{DomainName: "ext-pinned-a", Hosts: []compute.Host{
				{ComputeHost: "host-b"},
				{ComputeHost: "host-free"},
			}},
			expectedHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterDomainPinningStep{}
			step.Options = opts
			result, err := step.Run(logr.Discard(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Fatalf("expected %d hosts %v, got %d %v", len(tt.expectedHosts), tt.expectedHosts, len(result.Activations), result.Activations)
			}
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
		})
	}
}

func TestFilterDomainPinningStep_EmptyMapIsNoop(t *testing.T) {
	step := &FilterDomainPinningStep{}
	step.Options = FilterDomainPinningStepOpts{DomainHypervisors: map[string][]string{}}
	request := compute.PlacementRequest{
		DomainName: "ext-whatever",
		Hosts:      []compute.Host{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
	}
	result, err := step.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected both hosts to pass with empty mapping, got %d", len(result.Activations))
	}
}

func TestFilterDomainPinningStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        FilterDomainPinningStepOpts
		expectError bool
	}{
		{
			name:        "Valid mapping",
			opts:        FilterDomainPinningStepOpts{DomainHypervisors: map[string][]string{"ext-a": {"host1"}}},
			expectError: false,
		},
		{
			name:        "Empty mapping is allowed (noop)",
			opts:        FilterDomainPinningStepOpts{DomainHypervisors: map[string][]string{}},
			expectError: false,
		},
		{
			name:        "Empty host list for a domain",
			opts:        FilterDomainPinningStepOpts{DomainHypervisors: map[string][]string{"ext-a": {}}},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Errorf("expected validation error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no validation error but got: %v", err)
			}
		})
	}
}

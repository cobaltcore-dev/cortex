// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

func TestFilterExcludeHostsStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		excludedHosts []string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name:          "No excluded hosts - all hosts pass",
			excludedHosts: []string{},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "Single excluded host",
			excludedHosts: []string{"host2"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
		},
		{
			name:          "Multiple excluded hosts",
			excludedHosts: []string{"host1", "host3"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host2", "host4"},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name:          "All hosts excluded",
			excludedHosts: []string{"host1", "host2", "host3"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name:          "Excluded host not in request - no effect",
			excludedHosts: []string{"host-nonexistent"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name:          "Some excluded hosts in request, some not",
			excludedHosts: []string{"host1", "host-nonexistent", "host3"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name:          "Empty host list in request",
			excludedHosts: []string{"host1", "host2"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name:          "Nil excluded hosts list",
			excludedHosts: nil,
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name:          "Duplicate excluded hosts",
			excludedHosts: []string{"host1", "host1", "host2"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name:          "Single host request - excluded",
			excludedHosts: []string{"host1"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
		},
		{
			name:          "Single host request - not excluded",
			excludedHosts: []string{"host2"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name:          "Case sensitive matching",
			excludedHosts: []string{"Host1", "HOST2"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "Host1"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"Host1"},
		},
		{
			name:          "Hosts with special characters",
			excludedHosts: []string{"nova-compute-bb123.region.example.com"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "nova-compute-bb123.region.example.com"},
					{ComputeHost: "nova-compute-bb124.region.example.com"},
				},
			},
			expectedHosts: []string{"nova-compute-bb124.region.example.com"},
			filteredHosts: []string{"nova-compute-bb123.region.example.com"},
		},
		{
			name:          "Empty string in excluded hosts",
			excludedHosts: []string{"", "host1"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1"},
		},
		{
			name:          "Large number of excluded hosts",
			excludedHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6", "host7", "host8", "host9", "host10"},
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
					{ComputeHost: "host10"},
					{ComputeHost: "host11"},
					{ComputeHost: "host12"},
				},
			},
			expectedHosts: []string{"host11", "host12"},
			filteredHosts: []string{"host1", "host5", "host10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterExcludeHostsStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterExcludeHostsStepOpts]{
					BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterExcludeHostsStepOpts]{},
				},
			}
			step.Options = FilterExcludeHostsStepOpts{
				ExcludedHosts: tt.excludedHosts,
			}

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if result == nil {
				t.Fatal("expected result to be non-nil")
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

			// Verify statistics are present
			if result.Statistics == nil {
				t.Error("expected Statistics to be non-nil")
			}
		})
	}
}

func TestFilterExcludeHostsStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      FilterExcludeHostsStepOpts
		expectErr bool
	}{
		{
			name: "Empty excluded hosts",
			opts: FilterExcludeHostsStepOpts{
				ExcludedHosts: []string{},
			},
			expectErr: false,
		},
		{
			name: "Nil excluded hosts",
			opts: FilterExcludeHostsStepOpts{
				ExcludedHosts: nil,
			},
			expectErr: false,
		},
		{
			name: "Valid excluded hosts",
			opts: FilterExcludeHostsStepOpts{
				ExcludedHosts: []string{"host1", "host2"},
			},
			expectErr: false,
		},
		{
			name: "Single excluded host",
			opts: FilterExcludeHostsStepOpts{
				ExcludedHosts: []string{"host1"},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectErr && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("expected no error but got %v", err)
			}
		})
	}
}

func TestFilterExcludeHostsStep_Index(t *testing.T) {
	// Verify that the filter is registered in the Index
	factory, ok := Index["filter_exclude_hosts"]
	if !ok {
		t.Fatal("expected filter_exclude_hosts to be registered in Index")
	}

	filter := factory()
	if filter == nil {
		t.Fatal("expected factory to return a non-nil filter")
	}

	_, ok = filter.(*FilterExcludeHostsStep)
	if !ok {
		t.Fatal("expected factory to return a *FilterExcludeHostsStep")
	}
}

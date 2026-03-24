// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestFilterRequestedDestinationStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		hypervisors   []hv1.Hypervisor
		expectedHosts []string
		filteredHosts []string
		expectErr     bool
	}{
		{
			name: "No requested_destination - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: nil,
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate3"}}},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
			expectErr:     false,
		},
		{
			name: "Empty requested_destination - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{},
								Host:       "",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
			expectErr:     false,
		},
		{
			name: "Filter by specific host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Host: "host2",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3"},
			expectErr:     false,
		},
		{
			name: "Filter by single aggregate - hosts in spec aggregates",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}, {UUID: "aggregate2"}}},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Filter by single aggregate - hosts in status aggregates",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}, {UUID: "aggregate3"}}},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Filter by multiple aggregates",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1", "aggregate3"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate3"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host4"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate4"}}},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2", "host4"},
			expectErr:     false,
		},
		{
			name: "Filter by aggregates - hosts in both spec and status",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1", "aggregate2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}, {UUID: "aggregate3"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}, {UUID: "aggregate3"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate4"}, {UUID: "aggregate5"}}},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3"},
			expectErr:     false,
		},
		{
			name: "Filter by both host and aggregates - host takes precedence",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
								Host:       "host2",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3"},
			expectErr:     false,
		},
		{
			name: "Host not found in hypervisors - filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-nonexistent"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host-nonexistent"},
			expectErr:     false,
		},
		{
			name: "Host with no aggregates - filtered out when aggregates requested",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "All hosts filtered by aggregate",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"nonexistent-aggregate"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
			expectErr:     false,
		},
		{
			name: "Requested host not in candidates",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Host: "host-not-in-list",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate2"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host-not-in-list"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate3"}}},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
			expectErr:     false,
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"aggregate1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "aggregate1"}}},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
			expectErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with hypervisors
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add hv1 scheme: %v", err)
			}
			objs := make([]client.Object, len(tt.hypervisors))
			for i := range tt.hypervisors {
				objs[i] = &tt.hypervisors[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			step := &FilterRequestedDestinationStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
					BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
						Client: fakeClient,
					},
				},
			}

			result, err := step.Run(slog.Default(), tt.request)

			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if result == nil {
				t.Fatal("expected result to be non-nil")
				return
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

func TestFilterRequestedDestinationStep_processRequestedAggregates(t *testing.T) {
	tests := []struct {
		name              string
		aggregates        []string
		ignoredAggregates []string
		hypervisors       map[string]hv1.Hypervisor
		activations       map[string]float64
		expectedHosts     []string
	}{
		{
			name:       "empty aggregates - no filtering",
			aggregates: []string{},
			hypervisors: map[string]hv1.Hypervisor{
				"host1": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg1"}}}},
			},
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:              "all aggregates ignored - no filtering",
			aggregates:        []string{"agg1", "agg2"},
			ignoredAggregates: []string{"agg1", "agg2"},
			hypervisors: map[string]hv1.Hypervisor{
				"host1": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg1"}}}},
				"host2": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg3"}}}},
			},
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:       "filter by aggregate",
			aggregates: []string{"agg1"},
			hypervisors: map[string]hv1.Hypervisor{
				"host1": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg1"}}}},
				"host2": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg2"}}}},
			},
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{"host1"},
		},
		{
			name:       "unknown host filtered out",
			aggregates: []string{"agg1"},
			hypervisors: map[string]hv1.Hypervisor{
				"host1": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg1"}}}},
			},
			activations:   map[string]float64{"host1": 1.0, "unknown": 1.0},
			expectedHosts: []string{"host1"},
		},
		{
			name:              "partial ignore - some aggregates considered",
			aggregates:        []string{"agg1", "agg2"},
			ignoredAggregates: []string{"agg1"},
			hypervisors: map[string]hv1.Hypervisor{
				"host1": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg1"}}}},
				"host2": {Status: hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "agg2"}}}},
			},
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{"host2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterRequestedDestinationStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
					BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
						Options: FilterRequestedDestinationStepOpts{
							IgnoredAggregates: tt.ignoredAggregates,
						},
					},
				},
			}
			step.processRequestedAggregates(slog.Default(), tt.aggregates, tt.hypervisors, tt.activations)
			if len(tt.activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(tt.activations))
			}
			for _, host := range tt.expectedHosts {
				if _, ok := tt.activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
		})
	}
}

func TestFilterRequestedDestinationStep_processRequestedHost(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		ignoredHostnames []string
		activations      map[string]float64
		expectedHosts    []string
	}{
		{
			name:          "empty host - no filtering",
			host:          "",
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name:             "host in ignored list - no filtering",
			host:             "host1",
			ignoredHostnames: []string{"host1"},
			activations:      map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts:    []string{"host1", "host2"},
		},
		{
			name:          "filter to specific host",
			host:          "host2",
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0, "host3": 1.0},
			expectedHosts: []string{"host2"},
		},
		{
			name:          "requested host not in activations",
			host:          "host-missing",
			activations:   map[string]float64{"host1": 1.0, "host2": 1.0},
			expectedHosts: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterRequestedDestinationStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
					BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
						Options: FilterRequestedDestinationStepOpts{
							IgnoredHostnames: tt.ignoredHostnames,
						},
					},
				},
			}
			step.processRequestedHost(slog.Default(), tt.host, tt.activations)
			if len(tt.activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(tt.activations))
			}
			for _, host := range tt.expectedHosts {
				if _, ok := tt.activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
		})
	}
}

func TestFilterRequestedDestinationStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    FilterRequestedDestinationStepOpts
		wantErr bool
	}{
		{
			name:    "Empty options - valid",
			opts:    FilterRequestedDestinationStepOpts{},
			wantErr: false,
		},
		{
			name: "With ignored aggregates - valid",
			opts: FilterRequestedDestinationStepOpts{
				IgnoredAggregates: []string{"aggregate1", "aggregate2"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFilterRequestedDestinationStepOpts_Combined(t *testing.T) {
	tests := []struct {
		name              string
		request           api.ExternalSchedulerRequest
		hypervisors       []hv1.Hypervisor
		ignoredAggregates []string
		ignoredHostnames  []string
		expectedHosts     []string
		filteredHosts     []string
	}{
		{
			name: "Both aggregate and host ignored - all filtering skipped",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"az-west"},
								Host:       "host1",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-west"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-east"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-west"}}},
				},
			},
			ignoredAggregates: []string{"az-west"},
			ignoredHostnames:  []string{"host1"},
			expectedHosts:     []string{"host1", "host2", "host3"},
			filteredHosts:     []string{},
		},
		{
			name: "Some aggregates ignored with host not ignored - host filtering still applies after aggregate filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"az-west", "production"},
								Host:       "host1",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "production"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "production"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "staging"}}},
				},
			},
			// az-west is ignored, so only production is considered
			// host1 and host2 have production, host3 is filtered out
			// Then host filtering applies and only host1 remains
			ignoredAggregates: []string{"az-west"},
			ignoredHostnames:  []string{},
			expectedHosts:     []string{"host1"},
			filteredHosts:     []string{"host2", "host3"},
		},
		{
			// Regression test: when all requested aggregates are ignored, the aggregate
			// filtering short-circuits but the explicit host filtering must still apply.
			// This ensures the explicit host is kept even when aggregate filtering is skipped.
			name: "All aggregates ignored with explicit host - host filtering still applies",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
							Data: api.NovaRequestedDestination{
								Aggregates: []string{"az-west", "az-east"},
								Host:       "host2",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-west"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-east"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []hv1.Aggregate{{UUID: "az-west"}}},
				},
			},
			// All aggregates (az-west, az-east) are ignored, so aggregate filtering is skipped
			// But host filtering still applies and only host2 should remain
			ignoredAggregates: []string{"az-west", "az-east"},
			ignoredHostnames:  []string{},
			expectedHosts:     []string{"host2"},
			filteredHosts:     []string{"host1", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add hv1 scheme: %v", err)
			}
			objs := make([]client.Object, len(tt.hypervisors))
			for i := range tt.hypervisors {
				objs[i] = &tt.hypervisors[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			step := &FilterRequestedDestinationStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
					BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
						Client: fakeClient,
						Options: FilterRequestedDestinationStepOpts{
							IgnoredAggregates: tt.ignoredAggregates,
							IgnoredHostnames:  tt.ignoredHostnames,
						},
					},
				},
			}

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}

func TestFilterRequestedDestinationStep_Run_ClientError(t *testing.T) {
	request := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
					Data: api.NovaRequestedDestination{
						Aggregates: []string{"aggregate1"},
					},
				},
			},
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
		},
	}

	// Create a client that will fail on List operations
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hv1 scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return context.Canceled
			},
		}).
		Build()

	step := &FilterRequestedDestinationStep{
		BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
			BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]{
				Client: fakeClient,
			},
		},
	}

	_, err := step.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("expected error when client fails, got none")
	}
}

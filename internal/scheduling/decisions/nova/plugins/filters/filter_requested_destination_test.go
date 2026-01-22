// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate3"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1", "aggregate2"}},
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
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate1", "aggregate3"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate3"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host4"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate4"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate3"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate3"}},
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate4"}},
					Status:     hv1.HypervisorStatus{Aggregates: []string{"aggregate5"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{}},
					Status:     hv1.HypervisorStatus{Aggregates: []string{}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate2"}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host-not-in-list"},
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate3"}},
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
					Spec:       hv1.HypervisorSpec{Aggregates: []string{"aggregate1"}},
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
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]{
					Client: fakeClient,
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
		BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]{
			Client: fakeClient,
		},
	}

	_, err := step.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("expected error when client fails, got none")
	}
}

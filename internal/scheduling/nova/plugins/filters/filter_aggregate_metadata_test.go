// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestFilterAggregateMetadata_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		hypervisors   []hv1.Hypervisor
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No aggregates with filter_tenant_id - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
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
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "agg1", Metadata: map[string]string{}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "agg2", Metadata: map[string]string{"other_key": "value"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{},
					},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Host with filter_tenant_id matching project - host passes",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
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
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-agg", Metadata: map[string]string{"filter_tenant_id": "project-a"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "open-agg", Metadata: map[string]string{}}},
					},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Host with filter_tenant_id not matching project - host filtered",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-b",
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
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-agg", Metadata: map[string]string{"filter_tenant_id": "project-a"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "open-agg", Metadata: map[string]string{}}},
					},
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Host in multiple aggregates with different filter_tenant_id - project matches one",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-b",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{
							{Name: "agg1", Metadata: map[string]string{"filter_tenant_id": "project-a"}},
							{Name: "agg2", Metadata: map[string]string{"filter_tenant_id": "project-b"}},
						},
					},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Host in multiple aggregates - project matches none",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-c",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{
							{Name: "agg1", Metadata: map[string]string{"filter_tenant_id": "project-a"}},
							{Name: "agg2", Metadata: map[string]string{"filter_tenant_id": "project-b"}},
						},
					},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Mixed hosts - some restricted, some open",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
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
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-a", Metadata: map[string]string{"filter_tenant_id": "project-a"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-b", Metadata: map[string]string{"filter_tenant_id": "project-b"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "open", Metadata: map[string]string{}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host4"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{},
					},
				},
			},
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted", Metadata: map[string]string{"filter_tenant_id": "project-a"}}},
					},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "All hosts filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-nonexistent",
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
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-a", Metadata: map[string]string{"filter_tenant_id": "project-a"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "restricted-b", Metadata: map[string]string{"filter_tenant_id": "project-b"}}},
					},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Host not in hypervisor list - passes (no restrictions found)",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "open", Metadata: map[string]string{}}},
					},
				},
			},
			expectedHosts: []string{"host1", "host-unknown"},
			filteredHosts: []string{},
		},
		{
			name: "Aggregate with nil metadata - treated as no restriction",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project-a",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Aggregates: []hv1.Aggregate{{Name: "agg-nil-metadata", Metadata: nil}},
					},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add hv1 scheme: %v", err)
			}
			objs := make([]client.Object, len(tt.hypervisors))
			for i := range tt.hypervisors {
				objs[i] = &tt.hypervisors[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			step := &FilterAggregateMetadata{}
			step.Client = fakeClient

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

func TestFilterAggregateMetadata_Run_ClientError(t *testing.T) {
	request := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				ProjectID: "project-a",
			},
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
		},
	}

	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return context.Canceled
			},
		}).
		Build()

	step := &FilterAggregateMetadata{}
	step.Client = fakeClient

	_, err := step.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("expected error when client fails, got none")
	}
}

func TestFilterAggregateMetadata_IndexRegistration(t *testing.T) {
	factory, ok := Index["filter_aggregate_metadata"]
	if !ok {
		t.Fatal("expected filter_aggregate_metadata to be registered in Index")
	}
	filter := factory()
	if _, ok := filter.(*FilterAggregateMetadata); !ok {
		t.Errorf("expected factory to return *FilterAggregateMetadata, got %T", filter)
	}
}

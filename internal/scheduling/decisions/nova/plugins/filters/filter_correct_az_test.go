// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterCorrectAZStep_Run(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   "host1",
				Labels: map[string]string{"topology.kubernetes.io/zone": "az-1"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   "host2",
				Labels: map[string]string{"topology.kubernetes.io/zone": "az-1"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   "host3",
				Labels: map[string]string{"topology.kubernetes.io/zone": "az-2"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   "host4",
				Labels: map[string]string{"topology.kubernetes.io/zone": "az-3"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   "host5",
				Labels: map[string]string{},
			},
		},
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Filter hosts in az-1",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3", "host4", "host5"},
		},
		{
			name: "Filter hosts in az-2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-2",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Filter hosts in az-3",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-3",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "No hosts in requested AZ",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-nonexistent",
					},
				},
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
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterCorrectAZStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
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

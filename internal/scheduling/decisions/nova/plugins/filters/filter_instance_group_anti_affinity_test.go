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

func TestFilterInstanceGroupAntiAffinityStep_Run(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-1"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-2"},
					{ID: "vm-uuid-3"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-1"},
					{ID: "vm-uuid-2"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-4"},
					{ID: "vm-uuid-5"},
					{ID: "vm-uuid-6"},
				},
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
			name: "No instance group - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID:  "vm-uuid-new",
						InstanceGroup: nil,
					},
				},
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
			name: "Instance group with affinity policy - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
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
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Instance group with soft-anti-affinity policy - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "soft-anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy with empty members list - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{},
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
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - default max_server_per_host=1",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
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
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
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
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=3",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-4", "vm-uuid-5", "vm-uuid-6"},
								Rules: map[string]any{
									"max_server_per_host": 3,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host5"},
		},
		{
			name: "Anti-affinity policy - host running same VM (resize scenario)",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-1",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - all hosts filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Anti-affinity policy - mixed hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3", "vm-uuid-4", "vm-uuid-5"},
							},
						},
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
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4", "host5"},
		},
		{
			name: "Anti-affinity policy - empty request hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - host with non-member instances",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-100", "vm-uuid-101"},
							},
						},
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
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=2 with mixed instances",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
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
			expectedHosts: []string{"host1", "host2", "host3", "host5"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "Anti-affinity policy - with instance UUID and project ID",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						ProjectID:    "project-abc",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								UUID:      "ig-uuid-456",
								Policy:    "anti-affinity",
								Members:   []string{"vm-uuid-1"},
								ProjectID: "project-abc",
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
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - multiple members on same host with max=2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Anti-affinity policy - resize with VM on multiple hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-2",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
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
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - single host scenario",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - high max_server_per_host value",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 10,
								},
							},
						},
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
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterInstanceGroupAntiAffinityStep{}
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

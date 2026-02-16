// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterHasAcceleratorsStep_Run(t *testing.T) {
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
				Traits: []string{"COMPUTE_ACCELERATORS"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"COMPUTE_ACCELERATORS", "SOME_OTHER_TRAIT"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"SOME_OTHER_TRAIT"},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Traits: []string{"COMPUTE_ACCELERATORS", "CUSTOM_GPU"},
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
			name: "No accelerators requested - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{},
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
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Accelerators requested - filter to hosts with COMPUTE_ACCELERATORS trait",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
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
			name: "Accelerators requested with different device profile value",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "fpga-profile",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host5"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Accelerators requested - all hosts have the trait",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Accelerators requested - no hosts have the trait",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
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
			name: "Other extra specs present but no accelerator request - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:cpu_policy":     "dedicated",
									"quota:vif_inbound": "100000",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Accelerators requested with empty device profile value",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "",
								},
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
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Empty host list with accelerators requested",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
								},
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
			name: "Empty host list without accelerators requested",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{},
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
			name: "Mixed hosts with and without accelerators trait",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "custom-accelerator",
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
			expectedHosts: []string{"host1", "host2", "host5"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Accelerators with additional extra specs",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
									"hw:cpu_policy":        "dedicated",
									"hw:mem_page_size":     "large",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Host not in database with accelerators requested",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"accel:device_profile": "gpu-profile",
								},
							},
						},
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
			step := &FilterHasAcceleratorsStep{}
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

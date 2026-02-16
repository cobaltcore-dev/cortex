// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"strings"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCandidateGatherer_MutateWithAllCandidates(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}

	tests := []struct {
		name           string
		hypervisors    []client.Object
		request        api.ExternalSchedulerRequest
		expectError    bool
		errorContains  string
		expectedHosts  []string
		expectedWeight float64
	}{
		{
			name:        "missing hypervisor_type in flavor extra specs",
			hypervisors: []client.Object{},
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
			},
			expectError:   true,
			errorContains: "missing hypervisor_type in flavor extra specs",
		},
		{
			name:        "unsupported hypervisor type vmware",
			hypervisors: []client.Object{},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "VMware vCenter Server",
								},
							},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "cannot gather all placement candidates for hypervisor type",
		},
		{
			name:        "unsupported hypervisor type unknown",
			hypervisors: []client.Object{},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "unknown-type",
								},
							},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "cannot gather all placement candidates for hypervisor type",
		},
		{
			name: "successful gathering with qemu hypervisor type",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "host1",
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "host2",
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "host3",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "qemu",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{"host1", "host2", "host3"},
			expectedWeight: 0.0,
		},
		{
			name: "successful gathering with QEMU uppercase",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "host1",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "QEMU",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{"host1"},
			expectedWeight: 0.0,
		},
		{
			name: "successful gathering with ch hypervisor type",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ch-host1",
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ch-host2",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "ch",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{"ch-host1", "ch-host2"},
			expectedWeight: 0.0,
		},
		{
			name: "successful gathering with CH uppercase",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ch-host1",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "CH",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{"ch-host1"},
			expectedWeight: 0.0,
		},
		{
			name:        "empty hypervisor list",
			hypervisors: []client.Object{},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "qemu",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{},
			expectedWeight: 0.0,
		},
		{
			name: "mixed case Qemu",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "host1",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "Qemu",
								},
							},
						},
					},
				},
			},
			expectError:    false,
			expectedHosts:  []string{"host1"},
			expectedWeight: 0.0,
		},
		{
			name: "request with existing hosts and weights are overwritten",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: metav1.ObjectMeta{
						Name: "new-host",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "qemu",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "old-host", HypervisorHostname: "old-hv"},
				},
				Weights: map[string]float64{
					"old-host": 5.0,
				},
			},
			expectError:    false,
			expectedHosts:  []string{"new-host"},
			expectedWeight: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.hypervisors...).
				Build()

			gatherer := &candidateGatherer{Client: fakeClient}

			err := gatherer.MutateWithAllCandidates(context.Background(), &tt.request)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify hosts count
			if len(tt.request.Hosts) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(tt.request.Hosts))
			}

			// Verify weights count
			if len(tt.request.Weights) != len(tt.expectedHosts) {
				t.Errorf("expected %d weights, got %d", len(tt.expectedHosts), len(tt.request.Weights))
			}

			// Verify each expected host is present
			hostMap := make(map[string]api.ExternalSchedulerHost)
			for _, host := range tt.request.Hosts {
				hostMap[host.ComputeHost] = host
			}

			for _, expectedHost := range tt.expectedHosts {
				host, ok := hostMap[expectedHost]
				if !ok {
					t.Errorf("expected host %q not found in request hosts", expectedHost)
					continue
				}

				// Verify compute host matches hypervisor hostname for KVM
				if host.HypervisorHostname != expectedHost {
					t.Errorf("expected hypervisor hostname %q for host %q, got %q",
						expectedHost, expectedHost, host.HypervisorHostname)
				}

				// Verify weight is set to default
				weight, ok := tt.request.Weights[expectedHost]
				if !ok {
					t.Errorf("expected weight for host %q not found", expectedHost)
					continue
				}
				if weight != tt.expectedWeight {
					t.Errorf("expected weight %f for host %q, got %f",
						tt.expectedWeight, expectedHost, weight)
				}
			}
		})
	}
}

func TestCandidateGatherer_Interface(t *testing.T) {
	// Verify that candidateGatherer implements CandidateGatherer interface
	var _ CandidateGatherer = (*candidateGatherer)(nil)
}

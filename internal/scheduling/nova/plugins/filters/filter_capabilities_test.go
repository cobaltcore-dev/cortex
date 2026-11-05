// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHvToNovaCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		hv          hv1.Hypervisor
		expected    map[string]string
		expectError bool
	}{
		{
			name: "CH hypervisor with x86_64 architecture",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					DomainCapabilities: hv1.DomainCapabilities{
						HypervisorType: "ch",
					},
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
				},
			},
			expected: map[string]string{
				"capabilities:hypervisor_type": "CH",
				"capabilities:cpu_arch":        "x86_64",
			},
			expectError: false,
		},
		{
			name: "QEMU hypervisor with x86_64 architecture",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					DomainCapabilities: hv1.DomainCapabilities{
						HypervisorType: "kvm",
					},
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
				},
			},
			expected: map[string]string{
				"capabilities:hypervisor_type": "QEMU",
				"capabilities:cpu_arch":        "x86_64",
			},
			expectError: false,
		},
		{
			name: "CH hypervisor with aarch64 architecture",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					DomainCapabilities: hv1.DomainCapabilities{
						HypervisorType: "ch",
					},
					Capabilities: hv1.Capabilities{
						HostCpuArch: "aarch64",
					},
				},
			},
			expected: map[string]string{
				"capabilities:hypervisor_type": "CH",
				"capabilities:cpu_arch":        "aarch64",
			},
			expectError: false,
		},
		{
			name: "Unknown hypervisor type",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					DomainCapabilities: hv1.DomainCapabilities{
						HypervisorType: "bla",
					},
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
				},
			},
			expected:    nil,
			expectError: true,
		},
		{
			name: "Empty hypervisor type",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					DomainCapabilities: hv1.DomainCapabilities{
						HypervisorType: "",
					},
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
				},
			},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := hvToNovaCapabilities(tt.hv)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d capabilities, got %d", len(tt.expected), len(result))
			}
			for key, expectedValue := range tt.expected {
				if actualValue, ok := result[key]; !ok {
					t.Errorf("expected key %s not found in result", key)
				} else if actualValue != expectedValue {
					t.Errorf("for key %s, expected %s, got %s", key, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestFilterCapabilitiesStep_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	err := hv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "ch",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "x86_64",
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "kvm",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "x86_64",
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "ch",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "aarch64",
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "kvm",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "aarch64",
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host-empty-type",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "x86_64",
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host-unknown-type",
			},
			Status: hv1.HypervisorStatus{
				DomainCapabilities: hv1.DomainCapabilities{
					HypervisorType: "xen",
				},
				Capabilities: hv1.Capabilities{
					HostCpuArch: "x86_64",
				},
			},
		},
	}

	tests := []struct {
		name           string
		request        compute.PlacementRequest
		expectedHosts  []string
		filteredHosts  []string
		expectedEvents []lib.FilterWeigherPipelineStepEvent
	}{
		{
			name: "No extra specs in request - all hosts pass",
			request: compute.PlacementRequest{
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
			name: "Non-capability extra specs - all hosts pass",
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Match CH hypervisor type",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
				},
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
			name: "Match QEMU hypervisor type",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "QEMU"},
				},
				Hosts: []compute.Host{
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
			name: "Match x86_64 CPU architecture",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "cpu_arch", Value: "x86_64"},
				},
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
			name: "Match aarch64 CPU architecture",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "cpu_arch", Value: "aarch64"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host3", "host4"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Match both hypervisor type and CPU architecture",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
					{Name: "cpu_arch", Value: "x86_64"},
				},
				Hosts: []compute.Host{
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
			name: "Match QEMU with aarch64",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "QEMU"},
					{Name: "cpu_arch", Value: "aarch64"},
				},
				Hosts: []compute.Host{
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
			name: "Ignore hypervisors not in request even with unknown type",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts:  []string{"host1"},
			filteredHosts:  []string{},
			expectedEvents: []lib.FilterWeigherPipelineStepEvent{},
		},
		{
			name: "Candidate hypervisor with empty hypervisor type emits event and is filtered out",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host-empty-type"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host-empty-type"},
			expectedEvents: []lib.FilterWeigherPipelineStepEvent{
				{Name: "filter_capabilities_unknown_hypervisor_type", Labels: map[string]string{"hypervisor_type": ""}},
			},
		},
		{
			name: "Candidate hypervisor with unknown hypervisor type emits event and is filtered out",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host-unknown-type"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host-unknown-type"},
			expectedEvents: []lib.FilterWeigherPipelineStepEvent{
				{Name: "filter_capabilities_unknown_hypervisor_type", Labels: map[string]string{"hypervisor_type": "xen"}},
			},
		},
		{
			name: "No matching hosts",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "KVM"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Mixed capability and non-capability extra specs",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
					{Name: "cpu_arch", Value: "x86_64"},
				},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Empty host list",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "CH"},
				},
				Hosts: []compute.Host{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Case sensitive capability matching",
			request: compute.PlacementRequest{
				RequestedCapabilities: []api.Capability{
					{Name: "hypervisor_type", Value: "ch"}, // lowercase should not match
				},
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Unsupported operator in extra specs - skip filter",
			request: compute.PlacementRequest{
				// An unsupported comparison operator is dropped by the adapter,
				// so the filter sees no requested capabilities and is a no-op.
				RequestedCapabilities: nil,
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
			name: "Unsupported <in> operator - skip filter",
			request: compute.PlacementRequest{
				// An unsupported comparison operator is dropped by the adapter,
				// so the filter sees no requested capabilities and is a no-op.
				RequestedCapabilities: nil,
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "All hosts match when no capabilities requested",
			request: compute.PlacementRequest{
				Hosts: []compute.Host{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterCapabilitiesStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
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

			// Check events
			if len(result.Events) != len(tt.expectedEvents) {
				t.Errorf("expected %d events, got %d", len(tt.expectedEvents), len(result.Events))
			}
			for i, expectedEvent := range tt.expectedEvents {
				if i >= len(result.Events) {
					break
				}
				gotEvent := result.Events[i]
				if gotEvent.Name != expectedEvent.Name {
					t.Errorf("expected event name %q, got %q", expectedEvent.Name, gotEvent.Name)
				}
				if len(gotEvent.Labels) != len(expectedEvent.Labels) {
					t.Errorf("expected event labels %v, got %v", expectedEvent.Labels, gotEvent.Labels)
				}
				for k, v := range expectedEvent.Labels {
					if gotEvent.Labels[k] != v {
						t.Errorf("expected event label %q=%q, got %q", k, v, gotEvent.Labels[k])
					}
				}
			}
		})
	}
}

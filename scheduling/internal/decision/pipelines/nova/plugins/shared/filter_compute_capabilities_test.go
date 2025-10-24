// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestFilterComputeCapabilitiesStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock hypervisor data
	hypervisors := []any{
		&nova.Hypervisor{ID: "hv1", Hostname: "hypervisor1", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.1", ServiceID: "svc1", ServiceHost: "host1", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: `{"arch": "x86_64", "model": "Haswell", "features": ["sse", "avx"]}`},
		&nova.Hypervisor{ID: "hv2", Hostname: "hypervisor2", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.2", ServiceID: "svc2", ServiceHost: "host2", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: `{"arch": "aarch64", "model": "Cortex-A72", "features": ["neon"]}`},
		&nova.Hypervisor{ID: "hv3", Hostname: "hypervisor3", State: "up", Status: "enabled", HypervisorType: "VMware", HypervisorVersion: 6007000, HostIP: "192.168.1.3", ServiceID: "svc3", ServiceHost: "host3", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No capabilities requested",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:cpu_policy": "dedicated",
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
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Match x86_64 architecture",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:arch": "x86_64",
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
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Match hypervisor type",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "VMware",
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
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name: "Match multiple capabilities",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:arch":            "x86_64",
									"capabilities:hypervisor_type": "QEMU",
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
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "No matching capabilities",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:arch": "s390x",
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
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "Host without hypervisor data",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:arch": "x86_64",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host4"}, // Non-existent host
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterComputeCapabilitiesStep{}
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
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

func TestConvertToCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:   "Flat values",
			prefix: "capabilities:",
			input: map[string]any{
				"arch":  "x86_64",
				"model": "Haswell",
			},
			expected: map[string]any{
				"capabilities:arch":  "x86_64",
				"capabilities:model": "Haswell",
			},
		},
		{
			name:   "Nested values",
			prefix: "capabilities:",
			input: map[string]any{
				"arch": "x86_64",
				"maxphysaddr": map[string]any{
					"bits": 46,
				},
			},
			expected: map[string]any{
				"capabilities:arch":             "x86_64",
				"capabilities:maxphysaddr:bits": 46,
			},
		},
		{
			name:   "Deep nesting",
			prefix: "capabilities:",
			input: map[string]any{
				"topology": map[string]any{
					"sockets": 2,
					"cores": map[string]any{
						"per_socket": 8,
					},
				},
			},
			expected: map[string]any{
				"capabilities:topology:sockets":          2,
				"capabilities:topology:cores:per_socket": 8,
			},
		},
		{
			name:     "Empty input",
			prefix:   "capabilities:",
			input:    map[string]any{},
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToCapabilities(tt.prefix, tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d capabilities, got %d", len(tt.expected), len(result))
			}

			for key, expectedValue := range tt.expected {
				if actualValue, ok := result[key]; !ok {
					t.Errorf("expected capability %s not found, got result %v", key, result)
				} else if actualValue != expectedValue {
					t.Errorf("expected capability %s to be %v, got %v", key, expectedValue, actualValue)
				}
			}
		})
	}
}

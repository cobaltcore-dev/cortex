// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
)

func mockVMwareHostLabels(computeHost, az string) map[string]string {
	return map[string]string{
		"availability_zone":  az,
		"compute_host":       computeHost,
		"cpu_architecture":   "",
		"workload_type":      "",
		"enabled":            "false",
		"decommissioned":     "false",
		"external_customer":  "false",
		"disabled_reason":    "-",
		"pinned_projects":    "false",
		"pinned_project_ids": "",
	}
}

func TestVMwareHostGetHostLabels(t *testing.T) {
	str := func(s string) *string { return &s }

	tests := []struct {
		name string
		host vmwareHost
		want []string
	}{
		{
			name: "all optional fields nil",
			host: vmwareHost{compute.HostDetails{
				AvailabilityZone: "az1",
				ComputeHost:      "nova-compute-1",
				CPUArchitecture:  "cascade-lake",
				WorkloadType:     "general-purpose",
				Enabled:          true,
				Decommissioned:   false,
				ExternalCustomer: false,
				DisabledReason:   nil,
				PinnedProjects:   nil,
			}},
			want: []string{"az1", "nova-compute-1", "cascade-lake", "general-purpose", "true", "false", "false", "-", "false", ""},
		},
		{
			name: "disabled reason set",
			host: vmwareHost{compute.HostDetails{
				AvailabilityZone: "az2",
				ComputeHost:      "nova-compute-2",
				DisabledReason:   str("scheduled-maintenance"),
			}},
			want: []string{"az2", "nova-compute-2", "", "", "false", "false", "false", "scheduled-maintenance", "false", ""},
		},
		{
			name: "pinned projects set",
			host: vmwareHost{compute.HostDetails{
				AvailabilityZone: "az1",
				ComputeHost:      "nova-compute-3",
				PinnedProjects:   str("proj-a,proj-b"),
			}},
			want: []string{"az1", "nova-compute-3", "", "", "false", "false", "false", "-", "true", "proj-a,proj-b"},
		},
		{
			name: "decommissioned and external customer",
			host: vmwareHost{compute.HostDetails{
				AvailabilityZone: "az3",
				ComputeHost:      "nova-compute-4",
				Decommissioned:   true,
				ExternalCustomer: true,
			}},
			want: []string{"az3", "nova-compute-4", "", "", "false", "true", "true", "-", "false", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.host.getHostLabels()
			if len(got) != len(vmwareHostLabels) {
				t.Fatalf("getHostLabels() returned %d values, want %d (matching vmwareHostLabels)", len(got), len(vmwareHostLabels))
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("label[%d] (%s) = %q, want %q", i, vmwareHostLabels[i], got[i], want)
				}
			}
		})
	}
}

func TestIsKVMFlavor(t *testing.T) {
	tests := []struct {
		flavor string
		want   bool
	}{
		{"m1_k_small", true},
		{"hana_k_large", true},
		{"hana_small", false},
		{"hana_c128_m1600", false},
		{"hana_c128_m1600_v2", false},
		{"small", false},
		{"m1_large", false},
	}
	for _, tt := range tests {
		if got := isKVMFlavor(tt.flavor); got != tt.want {
			t.Errorf("isKVMFlavor(%q) = %v, want %v", tt.flavor, got, tt.want)
		}
	}
}

func TestFlavorCPUArchitecture(t *testing.T) {
	tests := []struct {
		flavor string
		want   string
	}{
		{"hana_c128_m1600_v2", "sapphire-rapids"},
		{"hana_c256_m3200_v2", "sapphire-rapids"},
		{"hana_c128_m1600", "cascade-lake"},
		{"hana_small", "cascade-lake"},
	}
	for _, tt := range tests {
		if got := flavorCPUArchitecture(tt.flavor); got != tt.want {
			t.Errorf("flavorCPUArchitecture(%q) = %q, want %q", tt.flavor, got, tt.want)
		}
	}
}

func TestVmwareBytesFromUnit(t *testing.T) {
	tests := []struct {
		amount float64
		unit   string
		want   float64
		errMsg string
	}{
		{1024, "MiB", 1024 * 1024 * 1024, ""},
		{1024, "MB", 1024 * 1024 * 1024, ""},
		{2, "GiB", 2 * 1024 * 1024 * 1024, ""},
		{2, "GB", 2 * 1024 * 1024 * 1024, ""},
		{1, "TiB", 1024 * 1024 * 1024 * 1024, ""},
		{512, "KiB", 512 * 1024, ""},
		{100, "B", 100, ""},
		{100, "", 100, ""},
		{1, "TB", 0, "unknown memory unit: TB"},
	}
	for _, tt := range tests {
		got, err := bytesFromUnit(tt.amount, tt.unit)
		if tt.errMsg != "" {
			if err == nil || err.Error() != tt.errMsg {
				t.Errorf("vmwareBytesFromUnit(%v, %q): expected error %q, got %v", tt.amount, tt.unit, tt.errMsg, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("vmwareBytesFromUnit(%v, %q): unexpected error: %v", tt.amount, tt.unit, err)
			continue
		}
		if got != tt.want {
			t.Errorf("vmwareBytesFromUnit(%v, %q) = %f, want %f", tt.amount, tt.unit, got, tt.want)
		}
	}
}

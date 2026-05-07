// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mockKVMHostLabels(host, az string) map[string]string {
	bb := "unknown"
	parts := strings.Split(host, "-")
	if len(parts) > 1 {
		bb = parts[1]
	}
	return map[string]string{
		"compute_host":      host,
		"availability_zone": az,
		"building_block":    bb,
		"cpu_architecture":  "cascade-lake",
		"workload_type":     "general-purpose",
		"enabled":           "true",
		"decommissioned":    "false",
		"external_customer": "false",
		"maintenance":       "false",
		"os_version":        "unknown",
	}
}

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

func TestVMwareHost_GetHostLabels(t *testing.T) {
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

func TestKVMHost_GetHostLabels(t *testing.T) {
	tests := []struct {
		name string
		host kvmHost
		want []string
	}{
		{
			name: "defaults with no traits and no labels",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "general-purpose", "true", "false", "false", "false"},
		},
		{
			name: "availability zone from label",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node001-bb01",
					Labels: map[string]string{"topology.kubernetes.io/zone": "az1"},
				},
			}},
			want: []string{"node001-bb01", "az1", "bb01", "cascade-lake", "general-purpose", "true", "false", "false", "false"},
		},
		{
			name: "name without dash results in unknown building block",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "nodewithoutdash"},
			}},
			want: []string{"nodewithoutdash", "unknown", "unknown", "cascade-lake", "general-purpose", "true", "false", "false", "false"},
		},
		{
			name: "sapphire rapids trait",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_HW_SAPPHIRE_RAPIDS"}},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "sapphire-rapids", "general-purpose", "true", "false", "false", "false"},
		},
		{
			name: "hana exclusive host trait",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_HANA_EXCLUSIVE_HOST"}},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "hana", "true", "false", "false", "false"},
		},
		{
			name: "decommissioning trait",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_DECOMMISSIONING"}},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "general-purpose", "true", "true", "false", "false"},
		},
		{
			name: "external customer exclusive trait",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"}},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "general-purpose", "true", "false", "true", "false"},
		},
		{
			name: "maintenance set",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Spec:       hv1.HypervisorSpec{Maintenance: hv1.MaintenanceManual},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "general-purpose", "true", "false", "false", "true"},
		},
		{
			name: "all traits and maintenance set",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node001-bb42",
					Labels: map[string]string{"topology.kubernetes.io/zone": "az3"},
				},
				Spec: hv1.HypervisorSpec{Maintenance: hv1.MaintenanceAuto},
				Status: hv1.HypervisorStatus{Traits: []string{
					"CUSTOM_HW_SAPPHIRE_RAPIDS",
					"CUSTOM_HANA_EXCLUSIVE_HOST",
					"CUSTOM_DECOMMISSIONING",
					"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE",
				}},
			}},
			want: []string{"node001-bb42", "az3", "bb42", "sapphire-rapids", "hana", "true", "true", "true", "true"},
		},
		{
			name: "os version set",
			host: kvmHost{hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bb01"},
				Spec:       hv1.HypervisorSpec{OperatingSystemVersion: "1.1.1"},
			}},
			want: []string{"node001-bb01", "unknown", "bb01", "cascade-lake", "general-purpose", "true", "false", "false", "false", "1.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.host.getHostLabels()
			if len(got) != len(kvmHostLabels) {
				t.Fatalf("getHostLabels() returned %d values, want %d (matching kvmHostLabels)", len(got), len(kvmHostLabels))
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("label[%d] (%s) = %q, want %q", i, kvmHostLabels[i], got[i], want)
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

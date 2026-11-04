// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"slices"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

func TestHypervisorTypeCapability(t *testing.T) {
	tests := []struct {
		name        string
		extraSpecs  map[string]string
		defaultQEMU bool
		want        []compute.Capability
	}{
		{
			name:       "value passed through",
			extraSpecs: map[string]string{"capabilities:hypervisor_type": "CH"},
			want:       []compute.Capability{{Name: "hypervisor_type", Value: "CH"}},
		},
		{
			name:       "lowercase value canonicalized to match hosts",
			extraSpecs: map[string]string{"capabilities:hypervisor_type": "qemu"},
			want:       []compute.Capability{{Name: "hypervisor_type", Value: "QEMU"}},
		},
		{
			name:       "missing without default -> nil",
			extraSpecs: map[string]string{},
			want:       nil,
		},
		{
			name:        "missing with default -> canonical QEMU",
			extraSpecs:  map[string]string{},
			defaultQEMU: true,
			want:        []compute.Capability{{Name: "hypervisor_type", Value: "QEMU"}},
		},
		{
			name:        "explicit value wins over default",
			extraSpecs:  map[string]string{"capabilities:hypervisor_type": "CH"},
			defaultQEMU: true,
			want:        []compute.Capability{{Name: "hypervisor_type", Value: "CH"}},
		},
		{
			name: "non-hypervisor_type capabilities preserved",
			extraSpecs: map[string]string{
				"capabilities:hypervisor_type": "qemu",
				"capabilities:cpu_arch":        "aarch64",
			},
			want: []compute.Capability{
				{Name: "cpu_arch", Value: "aarch64"},
				{Name: "hypervisor_type", Value: "QEMU"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HypervisorTypeCapability(tt.extraSpecs, tt.defaultQEMU)
			slices.SortFunc(got, func(a, b compute.Capability) int { return strings.Compare(a.Name, b.Name) })
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

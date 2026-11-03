// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
)

func TestExtractTraits(t *testing.T) {
	required, forbidden := ExtractTraits(map[string]string{
		"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
		"trait:CUSTOM_NO_GPU":              "forbidden",
		"trait:IGNORED":                    "preferred", // neither required nor forbidden
		"capabilities:hypervisor_type":     "qemu",      // not a trait
	})
	if !slices.Equal(required, []string{"CUSTOM_HANA_EXCLUSIVE_HOST"}) {
		t.Errorf("required = %v, want [CUSTOM_HANA_EXCLUSIVE_HOST]", required)
	}
	if !slices.Equal(forbidden, []string{"CUSTOM_NO_GPU"}) {
		t.Errorf("forbidden = %v, want [CUSTOM_NO_GPU]", forbidden)
	}
}

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
			name:       "missing without default -> nil",
			extraSpecs: map[string]string{},
			want:       nil,
		},
		{
			name:        "missing with default -> qemu",
			extraSpecs:  map[string]string{},
			defaultQEMU: true,
			want:        []compute.Capability{{Name: "hypervisor_type", Value: "qemu"}},
		},
		{
			name:        "explicit value wins over default",
			extraSpecs:  map[string]string{"capabilities:hypervisor_type": "CH"},
			defaultQEMU: true,
			want:        []compute.Capability{{Name: "hypervisor_type", Value: "CH"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HypervisorTypeCapability(tt.extraSpecs, tt.defaultQEMU)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

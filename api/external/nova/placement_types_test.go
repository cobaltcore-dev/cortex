// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitTraits(t *testing.T) {
	required, forbidden := SplitTraits(map[string]string{
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

func TestSplitCapabilities(t *testing.T) {
	sortCaps := func(c []Capability) {
		slices.SortFunc(c, func(a, b Capability) int { return strings.Compare(a.Name, b.Name) })
	}
	cases := []struct {
		name       string
		extraSpecs map[string]string
		want       []Capability
	}{
		{
			name:       "canonicalizes lowercase hypervisor_type to match hosts",
			extraSpecs: map[string]string{"capabilities:hypervisor_type": "qemu"},
			want:       []Capability{{Name: "hypervisor_type", Value: "QEMU"}},
		},
		{
			name:       "ch canonicalized",
			extraSpecs: map[string]string{"capabilities:hypervisor_type": "ch"},
			want:       []Capability{{Name: "hypervisor_type", Value: "CH"}},
		},
		{
			name: "non-hypervisor_type capabilities preserved verbatim",
			extraSpecs: map[string]string{
				"capabilities:hypervisor_type": "QEMU",
				"capabilities:cpu_arch":        "aarch64",
			},
			want: []Capability{
				{Name: "cpu_arch", Value: "aarch64"},
				{Name: "hypervisor_type", Value: "QEMU"},
			},
		},
		{
			name: "unsupported operator drops all capabilities",
			extraSpecs: map[string]string{
				"capabilities:hypervisor_type": "QEMU",
				"capabilities:cpu_arch":        "<in> aarch64 x86_64",
			},
			want: nil,
		},
		{
			name:       "non-capability extra specs ignored",
			extraSpecs: map[string]string{"trait:CUSTOM_X": "required", "quota:hw_version": "2101"},
			want:       nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitCapabilities(c.extraSpecs)
			sortCaps(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequestsAccelerators(t *testing.T) {
	if !RequestsAccelerators(map[string]string{"accel:device_profile": "gpu-v100"}) {
		t.Error("expected accelerators requested when accel:device_profile is present")
	}
	if RequestsAccelerators(map[string]string{"capabilities:cpu_arch": "x86_64"}) {
		t.Error("expected no accelerators requested without accel:device_profile")
	}
}

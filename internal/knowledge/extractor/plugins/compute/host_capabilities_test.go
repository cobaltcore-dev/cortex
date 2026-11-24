// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
)

func TestHostCapabilitiesExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		in       HostCapabilitiesInputs
		expected []HostCapabilities
	}{
		{
			name: "joins traits per compute host",
			in: HostCapabilitiesInputs{
				Hypervisors: []nova.Hypervisor{
					{ID: "uuid2", ServiceHost: "host2", HypervisorType: "QEMU"},
					{ID: "uuid1", ServiceHost: "host1", HypervisorType: "QEMU"},
					// Hypervisors without traits are skipped.
					{ID: "uuid3", ServiceHost: "host3", HypervisorType: "QEMU"},
				},
				Traits: []placement.Trait{
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_1"},
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_2"},
					{ResourceProviderUUID: "uuid2", Name: "TRAIT_3"},
					{ResourceProviderUUID: "unknown", Name: "TRAIT_4"},
				},
			},
			expected: []HostCapabilities{
				{ComputeHost: "host1", Traits: "TRAIT_1,TRAIT_2", HypervisorType: "QEMU"},
				{ComputeHost: "host2", Traits: "TRAIT_3", HypervisorType: "QEMU"},
			},
		},
		{
			name: "sorts traits of all hypervisors of a host and hypervisor type",
			in: HostCapabilitiesInputs{
				Hypervisors: []nova.Hypervisor{
					{ID: "uuid1", ServiceHost: "host1", HypervisorType: "VMware vCenter Server"},
					{ID: "uuid2", ServiceHost: "host1", HypervisorType: "VMware vCenter Server"},
					{ID: "uuid3", ServiceHost: "host1", HypervisorType: "ironic"},
				},
				Traits: []placement.Trait{
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_C"},
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_A"},
					{ResourceProviderUUID: "uuid2", Name: "TRAIT_B"},
					{ResourceProviderUUID: "uuid3", Name: "TRAIT_D"},
				},
			},
			expected: []HostCapabilities{
				{ComputeHost: "host1", Traits: "TRAIT_A,TRAIT_B,TRAIT_C", HypervisorType: "VMware vCenter Server"},
				{ComputeHost: "host1", Traits: "TRAIT_D", HypervisorType: "ironic"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, err := (&HostCapabilitiesExtractor{}).Extract(t.Context(), tt.in)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !slices.Equal(features, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, features)
			}
		})
	}
}

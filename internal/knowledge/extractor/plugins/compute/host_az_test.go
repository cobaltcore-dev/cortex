// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
)

func TestHostAZExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		in       HostAZInputs
		expected []HostAZ
	}{
		{
			name: "resolves availability zones from aggregates",
			in: HostAZInputs{
				Hypervisors: []nova.Hypervisor{
					{ID: "uuid4", ServiceHost: "host4"},
					{ID: "uuid1", ServiceHost: "host1"},
					{ID: "uuid2", ServiceHost: "host2"},
					{ID: "uuid3", ServiceHost: "host3"},
				},
				Aggregates: []nova.Aggregate{
					// Aggregates without availability zone are ignored.
					{UUID: "agg1", Name: "something_else", ComputeHost: new("host1"), Metadata: "{}"},
					{UUID: "agg2", Name: "az1", AvailabilityZone: new("az1"), ComputeHost: new("host1"), Metadata: "{}"},
					// Only an aggregate without availability zone for host2.
					{UUID: "agg3", Name: "something_else_again", ComputeHost: new("host2"), Metadata: "{}"},
					// No aggregate for host3.
					{UUID: "agg4", Name: "az2", AvailabilityZone: new("az2"), ComputeHost: new("host4"), Metadata: "{}"},
					// Aggregates without compute host are ignored.
					{UUID: "agg5", Name: "az3", AvailabilityZone: new("az3"), Metadata: "{}"},
				},
			},
			expected: []HostAZ{
				{ComputeHost: "host1", AvailabilityZone: new("az1")},
				{ComputeHost: "host2"},
				{ComputeHost: "host3"},
				{ComputeHost: "host4", AvailabilityZone: new("az2")},
			},
		},
		{
			name: "uses the lexicographically smallest of multiple availability zones",
			in: HostAZInputs{
				Hypervisors: []nova.Hypervisor{{ID: "uuid1", ServiceHost: "host1"}},
				Aggregates: []nova.Aggregate{
					{UUID: "agg1", AvailabilityZone: new("az-b"), ComputeHost: new("host1")},
					{UUID: "agg2", AvailabilityZone: new("az-a"), ComputeHost: new("host1")},
					{UUID: "agg3", AvailabilityZone: new("az-c"), ComputeHost: new("host1")},
				},
			},
			expected: []HostAZ{{ComputeHost: "host1", AvailabilityZone: new("az-a")}},
		},
		{
			name:     "no hypervisors",
			in:       HostAZInputs{Aggregates: []nova.Aggregate{{UUID: "agg1", AvailabilityZone: new("az1"), ComputeHost: new("host1")}}},
			expected: []HostAZ{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, err := (&HostAZExtractor{}).Extract(t.Context(), tt.in)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !reflect.DeepEqual(features, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, features)
			}
		})
	}
}

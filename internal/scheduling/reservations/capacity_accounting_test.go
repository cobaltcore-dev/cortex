// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestResourcesToBlock(t *testing.T) {
	gib := func(n int64) resource.Quantity { return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI) }
	memBytes := func(m map[hv1.ResourceName]resource.Quantity) int64 {
		q, ok := m[hv1.ResourceMemory]
		if !ok {
			return 0
		}
		return q.Value()
	}

	tests := []struct {
		name              string
		res               *v1alpha1.Reservation
		ignoreAllocations bool
		wantMemoryBytes   int64
	}{
		{
			name: "failover: full slot blocked",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeFailover,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
				},
			},
			wantMemoryBytes: 480 * 1024 * 1024 * 1024,
		},
		{
			name: "CR no allocations: full slot blocked",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:                         v1alpha1.ReservationTypeCommittedResource,
					Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
				},
			},
			wantMemoryBytes: 480 * 1024 * 1024 * 1024,
		},
		{
			name: "CR 1 confirmed VM (240Gi), slot=480Gi: remaining = 240Gi blocked",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						Allocations: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
						},
					},
				},
				Status: v1alpha1.ReservationStatus{
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocations: map[string]string{"vm-1": "host-a"},
					},
				},
			},
			wantMemoryBytes: 240 * 1024 * 1024 * 1024,
		},
		{
			name: "CR slot fully consumed by confirmed VMs: block = 0",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						Allocations: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
							"vm-2": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
						},
					},
				},
				Status: v1alpha1.ReservationStatus{
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocations: map[string]string{"vm-1": "host-a", "vm-2": "host-a"},
					},
				},
			},
			wantMemoryBytes: 0,
		},
		{
			name: "CR spec-only VM (240Gi), slot=480Gi, no confirmed: specOnly < remaining → full slot blocked",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						Allocations: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
						},
					},
				},
				// vm-1 not in status → spec-only
			},
			wantMemoryBytes: 480 * 1024 * 1024 * 1024,
		},
		{
			name: "CR mid-migration (TargetHost != Status.Host): full slot blocked despite confirmed VMs",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "new-host",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						Allocations: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
						},
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "old-host", // differs from TargetHost → migration in progress
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocations: map[string]string{"vm-1": "old-host"},
					},
				},
			},
			wantMemoryBytes: 480 * 1024 * 1024 * 1024,
		},
		{
			name: "CR ignoreAllocations=true: full slot blocked regardless of confirmed VMs",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						Allocations: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(240)}},
						},
					},
				},
				Status: v1alpha1.ReservationStatus{
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocations: map[string]string{"vm-1": "host-a"},
					},
				},
			},
			ignoreAllocations: true,
			wantMemoryBytes:   480 * 1024 * 1024 * 1024,
		},
		{
			name: "no memory resource: block = 0",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeFailover,
					Resources: map[hv1.ResourceName]resource.Quantity{},
				},
			},
			wantMemoryBytes: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memBytes(ResourcesToBlock(tt.res, tt.ignoreAllocations))
			if got != tt.wantMemoryBytes {
				t.Errorf("ResourcesToBlock() memory = %d, want %d", got, tt.wantMemoryBytes)
			}
		})
	}
}

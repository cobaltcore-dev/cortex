// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestUnusedReservationCapacity(t *testing.T) {
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
			got := memBytes(UnusedReservationCapacity(tt.res, tt.ignoreAllocations))
			if got != tt.wantMemoryBytes {
				t.Errorf("UnusedReservationCapacity() memory = %d, want %d", got, tt.wantMemoryBytes)
			}
		})
	}
}

func TestHostFreeCapacity(t *testing.T) {
	gib := func(n int64) resource.Quantity { return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI) }
	cpu := func(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

	hvWithCap := func(name string, memGiB, cpuCores int64) hv1.Hypervisor {
		return hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: hv1.HypervisorStatus{
				EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
			},
		}
	}
	crSlot := func(name, host string, memGiB, cpuCores int64) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: host,
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
			},
			Status: v1alpha1.ReservationStatus{Host: host},
		}
	}
	freeMemGiB := func(free map[hv1.ResourceName]resource.Quantity) int64 {
		q := free[hv1.ResourceMemory]
		return q.Value() / (1024 * 1024 * 1024)
	}
	freeCPU := func(free map[hv1.ResourceName]resource.Quantity) int64 {
		q := free[hv1.ResourceCPU]
		return q.Value()
	}

	t.Run("no capacity data returns nil", func(t *testing.T) {
		hv := hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host"}}
		if got := HostFreeCapacity(nil, hv); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("no reservations and no allocation: free = effective capacity", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		free := HostFreeCapacity(nil, hv)
		if freeMemGiB(free) != 1024 {
			t.Errorf("expected 1024 GiB free, got %d", freeMemGiB(free))
		}
		if freeCPU(free) != 256 {
			t.Errorf("expected 256 CPU free, got %d", freeCPU(free))
		}
	})

	t.Run("allocation subtracted from capacity", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: gib(512),
			hv1.ResourceCPU:    cpu(128),
		}
		free := HostFreeCapacity(nil, hv)
		if freeMemGiB(free) != 512 {
			t.Errorf("expected 512 GiB free, got %d", freeMemGiB(free))
		}
		if freeCPU(free) != 128 {
			t.Errorf("expected 128 CPU free, got %d", freeCPU(free))
		}
	})

	t.Run("reservation blocks subtracted", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		slots := []v1alpha1.Reservation{
			crSlot("slot-1", "host", 512, 128),
		}
		free := HostFreeCapacity(slots, hv)
		if freeMemGiB(free) != 512 {
			t.Errorf("expected 512 GiB free, got %d", freeMemGiB(free))
		}
		if freeCPU(free) != 128 {
			t.Errorf("expected 128 CPU free, got %d", freeCPU(free))
		}
	})

	t.Run("over-subscribed: negative free values", func(t *testing.T) {
		// 5 x 1TiB slots on a 4TiB host — the production scenario
		hv := hvWithCap("host", 4096, 256)
		slots := []v1alpha1.Reservation{
			crSlot("slot-0", "host", 1024, 128),
			crSlot("slot-1", "host", 1024, 128),
			crSlot("slot-2", "host", 1024, 128),
			crSlot("slot-3", "host", 1024, 128),
			crSlot("slot-4", "host", 1024, 128),
		}
		free := HostFreeCapacity(slots, hv)
		if freeMemGiB(free) != -1024 {
			t.Errorf("expected -1024 GiB (over-subscribed), got %d GiB", freeMemGiB(free))
		}
		if freeCPU(free) != -384 {
			t.Errorf("expected -384 CPU (over-subscribed), got %d", freeCPU(free))
		}
	})

	t.Run("allocation + reservations combined", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: gib(256),
			hv1.ResourceCPU:    cpu(64),
		}
		slots := []v1alpha1.Reservation{
			crSlot("slot-1", "host", 512, 128),
		}
		free := HostFreeCapacity(slots, hv)
		// 1024 - 256 (alloc) - 512 (slot) = 256 GiB free
		if freeMemGiB(free) != 256 {
			t.Errorf("expected 256 GiB free, got %d", freeMemGiB(free))
		}
		// 256 - 64 (alloc) - 128 (slot) = 64 free
		if freeCPU(free) != 64 {
			t.Errorf("expected 64 CPU free, got %d", freeCPU(free))
		}
	})

	t.Run("confirmed VM reduces slot block (not double counted)", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		// 256 GiB confirmed VM already counted in Allocation
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: gib(256),
		}
		// 512 GiB slot with 256 GiB confirmed VM → block = 512-256 = 256 GiB
		slot := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: "host",
				Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Allocations: map[string]v1alpha1.CommittedResourceAllocation{
						"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256)}},
					},
				},
			},
			Status: v1alpha1.ReservationStatus{
				Host: "host",
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
					Allocations: map[string]string{"vm-1": "host"},
				},
			},
		}
		free := HostFreeCapacity([]v1alpha1.Reservation{slot}, hv)
		// 1024 - 256 (alloc/vm) - 256 (remaining slot block) = 512
		if freeMemGiB(free) != 512 {
			t.Errorf("expected 512 GiB free, got %d", freeMemGiB(free))
		}
	})

	t.Run("reservations on other hosts are ignored even if passed in", func(t *testing.T) {
		hv := hvWithCap("host", 1024, 256)
		slots := []v1alpha1.Reservation{
			crSlot("slot-other", "other-host", 1024, 256), // different host — must not block
		}
		free := HostFreeCapacity(slots, hv)
		if freeMemGiB(free) != 1024 {
			t.Errorf("expected 1024 GiB free (other host ignored), got %d", freeMemGiB(free))
		}
	})

	t.Run("falls back to Capacity when EffectiveCapacity nil", func(t *testing.T) {
		hv := hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "host"},
			Status: hv1.HypervisorStatus{
				Capacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(512),
				},
			},
		}
		free := HostFreeCapacity(nil, hv)
		if freeMemGiB(free) != 512 {
			t.Errorf("expected 512 GiB, got %d", freeMemGiB(free))
		}
	})
}

func TestHostHasCapacityForReservation(t *testing.T) {
	gib := func(n int64) resource.Quantity { return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI) }
	cpu := func(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

	hvWithCapacity := func(name string, memGiB, cpuCores int64) hv1.Hypervisor {
		return hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: hv1.HypervisorStatus{
				EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
			},
		}
	}

	resWithSlot := func(name, targetHost string, memGiB, cpuCores int64) *v1alpha1.Reservation {
		return &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: targetHost,
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
			},
			Status: v1alpha1.ReservationStatus{Host: targetHost},
		}
	}
	deref := func(r *v1alpha1.Reservation) v1alpha1.Reservation { return *r }

	tests := []struct {
		name     string
		hv       hv1.Hypervisor
		res      *v1alpha1.Reservation
		others   []v1alpha1.Reservation
		wantFits bool
	}{
		{
			name:     "empty host: slot fits easily",
			hv:       hvWithCapacity("host-new", 960, 80),
			res:      resWithSlot("res-1", "host-old", 480, 40),
			wantFits: true,
		},
		{
			name: "host fully consumed by another reservation: no capacity",
			hv:   hvWithCapacity("host-new", 480, 40),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				deref(resWithSlot("res-blocker", "host-new", 480, 40)),
			},
			wantFits: false,
		},
		{
			name: "host partially consumed, enough room left",
			hv:   hvWithCapacity("host-new", 960, 80),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				deref(resWithSlot("res-blocker", "host-new", 480, 40)),
			},
			wantFits: true,
		},
		{
			name: "host partially consumed, exactly at boundary: fits",
			hv:   hvWithCapacity("host-new", 960, 80),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				deref(resWithSlot("res-blocker-a", "host-new", 240, 20)),
				deref(resWithSlot("res-blocker-b", "host-new", 240, 20)),
			},
			wantFits: true,
		},
		{
			name: "host partially consumed, one resource short (CPU)",
			hv:   hvWithCapacity("host-new", 960, 60),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				deref(resWithSlot("res-blocker", "host-new", 480, 40)),
			},
			// 960-480=480 memory OK, but 60-40=20 CPU < 40 required
			wantFits: false,
		},
		{
			name: "target reservation itself excluded from blocking calculation",
			hv:   hvWithCapacity("host-new", 480, 40),
			res:  resWithSlot("res-target", "host-new", 480, 40),
			others: []v1alpha1.Reservation{
				// Same name as res — should be ignored
				deref(resWithSlot("res-target", "host-new", 480, 40)),
			},
			wantFits: true,
		},
		{
			name: "reservations on other hosts do not count",
			hv:   hvWithCapacity("host-new", 480, 40),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				deref(resWithSlot("res-on-other-host", "host-unrelated", 480, 40)),
			},
			wantFits: true,
		},
		{
			name: "hv with no capacity data: always false",
			hv: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host-nocap"},
			},
			res:      resWithSlot("res-target", "host-old", 480, 40),
			wantFits: false,
		},
		{
			name: "hv allocation already consumed memory: no room",
			hv: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host-new"},
				Status: hv1.HypervisorStatus{
					EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: gib(480),
						hv1.ResourceCPU:    cpu(40),
					},
					Allocation: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: gib(100),
					},
				},
			},
			res:      resWithSlot("res-target", "host-old", 480, 40),
			wantFits: false, // 480-100 = 380 GiB remaining < 480 GiB slot (no confirmed VMs, so full slot is required)
		},
		{
			name: "reservation targeting via Status.Host (not TargetHost) still blocks",
			hv:   hvWithCapacity("host-new", 480, 40),
			res:  resWithSlot("res-target", "host-old", 480, 40),
			others: []v1alpha1.Reservation{
				// TargetHost empty but Status.Host = host-new
				{
					ObjectMeta: metav1.ObjectMeta{Name: "res-status-host"},
					Spec: v1alpha1.ReservationSpec{
						Type: v1alpha1.ReservationTypeCommittedResource,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: gib(480),
							hv1.ResourceCPU:    cpu(40),
						},
					},
					Status: v1alpha1.ReservationStatus{Host: "host-new"},
				},
			},
			wantFits: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HostHasCapacityForReservation(tt.others, tt.hv, tt.res)
			if got != tt.wantFits {
				t.Errorf("HostHasCapacityForReservation() = %v, want %v", got, tt.wantFits)
			}
		})
	}
}

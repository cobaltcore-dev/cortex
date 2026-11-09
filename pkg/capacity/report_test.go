// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func gib(n int64) resource.Quantity {
	return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI)
}
func cpu(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

func memGiB(m map[hv1.ResourceName]resource.Quantity) int64 {
	q := m[hv1.ResourceMemory]
	return q.Value() / (1024 * 1024 * 1024)
}
func cpuCores(m map[hv1.ResourceName]resource.Quantity) int64 {
	q := m[hv1.ResourceCPU]
	return q.Value()
}

func hvWithCap(memGiBVal, cpuVal int64) hv1.Hypervisor {
	return hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host"},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: gib(memGiBVal),
				hv1.ResourceCPU:    cpu(cpuVal),
			},
		},
	}
}

func crSlot(name, host string, memGiBVal, cpuVal int64) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: gib(memGiBVal),
				hv1.ResourceCPU:    cpu(cpuVal),
			},
		},
		Status: v1alpha1.ReservationStatus{Host: host},
	}
}

func TestReservationBlock(t *testing.T) {
	memBytes := func(m map[hv1.ResourceName]resource.Quantity) int64 {
		q := m[hv1.ResourceMemory]
		return q.Value()
	}
	crWithAllocations := func(slotGiB int64, allocGiB ...int64) v1alpha1.Reservation {
		alloc := map[string]v1alpha1.CommittedResourceAllocation{}
		for i, a := range allocGiB {
			alloc[string(rune('a'+i))] = v1alpha1.CommittedResourceAllocation{
				Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(a)},
			}
		}
		return v1alpha1.Reservation{
			Spec: v1alpha1.ReservationSpec{
				Type:                         v1alpha1.ReservationTypeCommittedResource,
				Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(slotGiB)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
			},
			Status: v1alpha1.ReservationStatus{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{Allocation: alloc},
			},
		}
	}

	tests := []struct {
		name              string
		res               v1alpha1.Reservation
		ignoreAllocations bool
		wantMemGiB        int64
	}{
		{
			name:       "failover: full slot blocked",
			res:        v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)}}},
			wantMemGiB: 480,
		},
		{
			name:       "failover host: nothing blocked",
			res:        v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailoverHost, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)}}},
			wantMemGiB: 0,
		},
		{
			name:       "CR no allocations: full slot blocked",
			res:        crWithAllocations(480),
			wantMemGiB: 480,
		},
		{
			name:       "CR 1 confirmed VM fills slot: block = 0",
			res:        crWithAllocations(480, 480),
			wantMemGiB: 0,
		},
		{
			name:       "CR 2 confirmed VMs fill slot: block = 0",
			res:        crWithAllocations(480, 240, 240),
			wantMemGiB: 0,
		},
		{
			name:       "CR half-filled: block = remainder",
			res:        crWithAllocations(480, 240),
			wantMemGiB: 240,
		},
		{
			name:              "CR ignoreAllocations: full slot blocked despite confirmed VMs",
			res:               crWithAllocations(480, 480),
			ignoreAllocations: true,
			wantMemGiB:        480,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memBytes(ReservationBlock(tt.res, tt.ignoreAllocations)) / (1024 * 1024 * 1024)
			if got != tt.wantMemGiB {
				t.Errorf("ReservationBlock() memory = %d GiB, want %d", got, tt.wantMemGiB)
			}
		})
	}

	t.Run("CR mid-migration: full slot blocked despite confirmed VMs", func(t *testing.T) {
		res := crWithAllocations(480, 480)
		res.Spec.TargetHost = "new-host"
		res.Status.Host = "old-host"
		if got := memBytes(ReservationBlock(res, false)) / (1024 * 1024 * 1024); got != 480 {
			t.Errorf("expected full 480 GiB blocked mid-migration, got %d", got)
		}
	})
}

func TestNewHostUsageReport(t *testing.T) {
	t.Run("no capacity data returns zero report", func(t *testing.T) {
		rep := NewHostUsageReport(hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host"}}, nil, false)
		if rep.Available != nil || rep.Total != nil {
			t.Errorf("expected zero report with nil maps, got %+v", rep)
		}
	})

	t.Run("empty host: available = effective capacity", func(t *testing.T) {
		rep := NewHostUsageReport(hvWithCap(1024, 256), nil, false)
		if memGiB(rep.Available) != 1024 || cpuCores(rep.Available) != 256 {
			t.Errorf("expected 1024 GiB / 256 CPU available, got %d / %d", memGiB(rep.Available), cpuCores(rep.Available))
		}
	})

	t.Run("allocation subtracted, recorded in Used", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512), hv1.ResourceCPU: cpu(128)}
		rep := NewHostUsageReport(hv, nil, false)
		if memGiB(rep.Available) != 512 || memGiB(rep.Used) != 512 {
			t.Errorf("expected 512 GiB available & used, got available=%d used=%d", memGiB(rep.Available), memGiB(rep.Used))
		}
	})

	t.Run("ignoreAllocations skips Used", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}
		rep := NewHostUsageReport(hv, nil, true)
		if memGiB(rep.Available) != 1024 || memGiB(rep.Used) != 0 {
			t.Errorf("expected allocation ignored, got available=%d used=%d", memGiB(rep.Available), memGiB(rep.Used))
		}
	})

	t.Run("CR reservation recorded in Reserved, subtracted from Available", func(t *testing.T) {
		rep := NewHostUsageReport(hvWithCap(1024, 256), []v1alpha1.Reservation{crSlot("slot-1", "host", 512, 128)}, false)
		if memGiB(rep.Available) != 512 || memGiB(rep.Reserved) != 512 {
			t.Errorf("expected 512 available & reserved, got available=%d reserved=%d", memGiB(rep.Available), memGiB(rep.Reserved))
		}
	})

	t.Run("failover reservation recorded in Failover, subtracted from Available", func(t *testing.T) {
		failover := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "fo"},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeFailover,
				TargetHost: "host",
				Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256), hv1.ResourceCPU: cpu(64)},
			},
			Status: v1alpha1.ReservationStatus{Host: "host"},
		}
		rep := NewHostUsageReport(hvWithCap(1024, 256), []v1alpha1.Reservation{failover}, false)
		if memGiB(rep.Available) != 768 || memGiB(rep.Failover) != 256 {
			t.Errorf("expected 768 available & 256 failover, got available=%d failover=%d", memGiB(rep.Available), memGiB(rep.Failover))
		}
	})

	t.Run("over-subscribed: Available clamped, shortfall in Oversubscribed", func(t *testing.T) {
		slots := []v1alpha1.Reservation{
			crSlot("s0", "host", 1024, 128), crSlot("s1", "host", 1024, 128),
			crSlot("s2", "host", 1024, 128), crSlot("s3", "host", 1024, 128),
			crSlot("s4", "host", 1024, 128),
		}
		rep := NewHostUsageReport(hvWithCap(4096, 256), slots, false)
		if memGiB(rep.Available) != 0 {
			t.Errorf("expected 0 available, got %d", memGiB(rep.Available))
		}
		if memGiB(rep.Oversubscribed) != 1024 {
			t.Errorf("expected 1024 GiB over-subscribed, got %d", memGiB(rep.Oversubscribed))
		}
	})

	t.Run("allocation + reservation combined", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256), hv1.ResourceCPU: cpu(64)}
		rep := NewHostUsageReport(hv, []v1alpha1.Reservation{crSlot("slot-1", "host", 512, 128)}, false)
		if memGiB(rep.Available) != 256 || cpuCores(rep.Available) != 64 {
			t.Errorf("expected 256 GiB / 64 CPU available, got %d / %d", memGiB(rep.Available), cpuCores(rep.Available))
		}
	})

	t.Run("confirmed VM reduces slot block, not double counted", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256)}
		slot := crSlot("slot-1", "host", 512, 0)
		slot.Spec.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationSpec{}
		slot.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{
			Allocation: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-1": {ObjectRef: corev1.ObjectReference{Name: "vm-1"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}},
			},
		}
		rep := NewHostUsageReport(hv, []v1alpha1.Reservation{slot}, false)
		if memGiB(rep.Available) != 768 { // 1024 - 256 alloc - 0 block
			t.Errorf("expected 768 GiB available, got %d", memGiB(rep.Available))
		}
	})

	t.Run("reservations on other hosts ignored", func(t *testing.T) {
		rep := NewHostUsageReport(hvWithCap(1024, 256), []v1alpha1.Reservation{crSlot("other", "other-host", 1024, 256)}, false)
		if memGiB(rep.Available) != 1024 {
			t.Errorf("expected 1024 GiB available, got %d", memGiB(rep.Available))
		}
	})

	t.Run("evicted reservation (empty TargetHost) does not block even if Status.Host set", func(t *testing.T) {
		evicted := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "evicted"},
			Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480), hv1.ResourceCPU: cpu(40)}},
			Status:     v1alpha1.ReservationStatus{Host: "host"},
		}
		rep := NewHostUsageReport(hvWithCap(1024, 256), []v1alpha1.Reservation{evicted}, false)
		if memGiB(rep.Reserved) != 0 {
			t.Errorf("expected 0 GiB reserved (evicted), got %d", memGiB(rep.Reserved))
		}
	})

	t.Run("falls back to Capacity when EffectiveCapacity nil", func(t *testing.T) {
		hv := hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "host"},
			Status:     hv1.HypervisorStatus{Capacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}},
		}
		if memGiB(NewHostUsageReport(hv, nil, false).Available) != 512 {
			t.Error("expected fallback to Capacity")
		}
	})

	t.Run("inputs not mutated", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256)}
		NewHostUsageReport(hv, []v1alpha1.Reservation{crSlot("slot-1", "host", 512, 128)}, false)
		if memGiB(hv.Status.EffectiveCapacity) != 1024 || memGiB(hv.Status.Allocation) != 256 {
			t.Error("hypervisor status was mutated")
		}
	})
}

func TestFits(t *testing.T) {
	demand := func(memGiBVal, cpuVal int64) map[hv1.ResourceName]resource.Quantity {
		return map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(memGiBVal), hv1.ResourceCPU: cpu(cpuVal)}
	}
	rep := NewHostUsageReport(hvWithCap(1024, 256), nil, false)
	tests := []struct {
		name   string
		demand map[hv1.ResourceName]resource.Quantity
		want   bool
	}{
		{"fits", demand(480, 40), true},
		{"exact boundary fits", demand(1024, 256), true},
		{"one over on memory", demand(1025, 40), false},
		{"empty demand no constraint", map[hv1.ResourceName]resource.Quantity{}, true},
		{"demand for unknown resource fails", map[hv1.ResourceName]resource.Quantity{"disk": gib(1)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rep.Fits(tt.demand); got != tt.want {
				t.Errorf("Fits() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("reservation reduces Available so demand no longer fits", func(t *testing.T) {
		full := NewHostUsageReport(hvWithCap(512, 64), []v1alpha1.Reservation{crSlot("slot", "host", 512, 64)}, false)
		if full.Fits(demand(256, 32)) {
			t.Error("fully reserved host should not fit a 256Gi demand")
		}
	})
}

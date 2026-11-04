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

func hvWithEffCap(name string, memGiB, cpuCores int64) hv1.Hypervisor {
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

func crSlot(name, host string, memGiB, cpuCores int64) v1alpha1.Reservation {
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

func memGiB(m map[hv1.ResourceName]resource.Quantity) int64 {
	q := m[hv1.ResourceMemory]
	return q.Value() / (1024 * 1024 * 1024)
}
func cpus(m map[hv1.ResourceName]resource.Quantity) int64 {
	q := m[hv1.ResourceCPU]
	return q.Value()
}

// mustDescribe calls DescribeHostUsage and fails the test on an unexpected error.
func mustDescribe(t *testing.T, hv hv1.Hypervisor, res []v1alpha1.Reservation, opts Options) HostUsage {
	t.Helper()
	u, err := DescribeHostUsage(hv, res, opts)
	if err != nil {
		t.Fatalf("DescribeHostUsage() unexpected error: %v", err)
	}
	return u
}

func TestDescribeHostUsage_NoData(t *testing.T) {
	_, err := DescribeHostUsage(hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "h"}}, nil, Options{})
	if err == nil {
		t.Fatal("expected ErrNoCapacityData, got nil")
	}
}

func TestDescribeHostUsage_CapacityFallback(t *testing.T) {
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "h"},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)},
		},
	}
	b := mustDescribe(t, hv, nil, Options{})
	if memGiB(b.Free) != 512 {
		t.Errorf("expected 512 GiB free from Capacity fallback, got %d", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_AllocationSubtracted(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: gib(512),
		hv1.ResourceCPU:    cpu(128),
	}
	b := mustDescribe(t, hv, nil, Options{})
	if memGiB(b.Free) != 512 || cpus(b.Free) != 128 {
		t.Errorf("free = %d GiB / %d CPU, want 512 / 128", memGiB(b.Free), cpus(b.Free))
	}
	if memGiB(b.Allocated) != 512 {
		t.Errorf("Allocated = %d GiB, want 512", memGiB(b.Allocated))
	}
}

func TestDescribeHostUsage_IgnoreAllocations(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}
	b := mustDescribe(t, hv, nil, Options{IgnoreAllocations: true})
	if memGiB(b.Free) != 1024 {
		t.Errorf("free = %d GiB, want 1024 (allocations ignored)", memGiB(b.Free))
	}
	if len(b.Allocated) != 0 {
		t.Errorf("Allocated should be empty, got %+v", b.Allocated)
	}
}

func TestDescribeHostUsage_CommittedVsFailoverSplit(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	failover := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "fo"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: "h",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: gib(64), hv1.ResourceCPU: cpu(16),
			},
		},
		Status: v1alpha1.ReservationStatus{Host: "h"},
	}
	committed := crSlot("cr", "h", 128, 32)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{failover, committed}, Options{})
	if memGiB(b.ReservedFailover) != 64 || cpus(b.ReservedFailover) != 16 {
		t.Errorf("failover = %d GiB / %d CPU, want 64 / 16", memGiB(b.ReservedFailover), cpus(b.ReservedFailover))
	}
	if memGiB(b.ReservedCommitted) != 128 || cpus(b.ReservedCommitted) != 32 {
		t.Errorf("committed = %d GiB / %d CPU, want 128 / 32", memGiB(b.ReservedCommitted), cpus(b.ReservedCommitted))
	}
	// Free = 1024 - 64 - 128 = 832, 256 - 16 - 32 = 208
	if memGiB(b.Free) != 832 || cpus(b.Free) != 208 {
		t.Errorf("free = %d GiB / %d CPU, want 832 / 208", memGiB(b.Free), cpus(b.Free))
	}
}

func TestDescribeHostUsage_MultipleSameTypeSummed(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{
		crSlot("cr-1", "h", 100, 10),
		crSlot("cr-2", "h", 50, 5),
	}, Options{})
	if memGiB(b.ReservedCommitted) != 150 || cpus(b.ReservedCommitted) != 15 {
		t.Errorf("committed sum = %d GiB / %d CPU, want 150 / 15", memGiB(b.ReservedCommitted), cpus(b.ReservedCommitted))
	}
}

func TestDescribeHostUsage_OtherHostIgnored(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{crSlot("other", "other-host", 1024, 256)}, Options{})
	if memGiB(b.Free) != 1024 {
		t.Errorf("free = %d GiB, want 1024 (other host ignored)", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_OverSubscribedNegative(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{crSlot("cr", "h", 2048, 512)}, Options{})
	freeMem := b.Free[hv1.ResourceMemory]
	if freeMem.Value() >= 0 {
		t.Errorf("expected negative free memory, got %d", freeMem.Value())
	}
}

func TestDescribeHostUsage_ClampToZero(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{crSlot("cr", "h", 2048, 512)}, Options{ClampToZero: true})
	if memGiB(b.Free) != 0 || cpus(b.Free) != 0 {
		t.Errorf("free = %d GiB / %d CPU, want 0 / 0 (clamped)", memGiB(b.Free), cpus(b.Free))
	}
}

func TestDescribeHostUsage_IgnoredReservationTypes(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{crSlot("cr", "h", 512, 128)}, Options{
		IgnoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
	})
	if memGiB(b.Free) != 1024 {
		t.Errorf("free = %d GiB, want 1024 (type ignored)", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_UnplacedSkippedByDefault(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	// TargetHost empty, Status.Host = h. Default BlockUnplacedReservations=false skips it.
	res := crSlot("cr", "", 512, 128)
	res.Status.Host = "h"
	b := mustDescribe(t, hv, []v1alpha1.Reservation{res}, Options{})
	if memGiB(b.Free) != 1024 {
		t.Errorf("free = %d GiB, want 1024 (unplaced skipped)", memGiB(b.Free))
	}
	// With BlockUnplacedReservations it blocks on Status.Host.
	b = mustDescribe(t, hv, []v1alpha1.Reservation{res}, Options{BlockUnplacedReservations: true})
	if memGiB(b.Free) != 512 {
		t.Errorf("free = %d GiB, want 512 (unplaced blocked on Status.Host)", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_ExcludeReservation(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	target := crSlot("target", "h", 512, 128)
	b := mustDescribe(t, hv, []v1alpha1.Reservation{target}, Options{ExcludeReservation: &target})
	if memGiB(b.Free) != 1024 {
		t.Errorf("free = %d GiB, want 1024 (excluded reservation not blocked)", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_ConfirmedVMNotDoubleCounted(t *testing.T) {
	hv := hvWithEffCap("h", 1024, 256)
	hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(256)}
	// 512 GiB slot, fully filled by one confirmed VM → block = 0.
	slot := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "cr"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "h",
			Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "h",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocation: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-1": {
						ObjectRef: corev1.ObjectReference{Name: "vm-1"},
						Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)},
					},
				},
			},
		},
	}
	b := mustDescribe(t, hv, []v1alpha1.Reservation{slot}, Options{})
	// 1024 - 256 (alloc) - 0 (slot filled) = 768
	if memGiB(b.Free) != 768 {
		t.Errorf("free = %d GiB, want 768", memGiB(b.Free))
	}
}

func TestDescribeHostUsage_MidMigrationFullSlot(t *testing.T) {
	hv := hvWithEffCap("new-host", 1024, 256)
	// TargetHost != Status.Host → mid-migration → full slot blocked despite confirmed VMs.
	slot := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "cr"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "new-host",
			Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "old-host",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocation: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)}},
				},
			},
		},
	}
	b := mustDescribe(t, hv, []v1alpha1.Reservation{slot}, Options{})
	if memGiB(b.ReservedCommitted) != 480 {
		t.Errorf("committed = %d GiB, want 480 (full slot, mid-migration)", memGiB(b.ReservedCommitted))
	}
}

func TestUnusedReservationCapacity(t *testing.T) {
	memBytes := func(m map[hv1.ResourceName]resource.Quantity) int64 {
		q := m[hv1.ResourceMemory]
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
			res: &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
				Type:      v1alpha1.ReservationTypeFailover,
				Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
			}},
			wantMemoryBytes: 480 * 1024 * 1024 * 1024,
		},
		{
			name: "failover host: nothing blocked",
			res: &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
				Type:      v1alpha1.ReservationTypeFailoverHost,
				Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
			}},
			wantMemoryBytes: 0,
		},
		{
			name: "CR fully consumed by confirmed VM: block 0",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
				},
				Status: v1alpha1.ReservationStatus{
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocation: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)}},
						},
					},
				},
			},
			wantMemoryBytes: 0,
		},
		{
			name: "CR ignoreAllocations: full slot blocked",
			res: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:      v1alpha1.ReservationTypeCommittedResource,
					Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)},
				},
				Status: v1alpha1.ReservationStatus{
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocation: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(480)}},
						},
					},
				},
			},
			ignoreAllocations: true,
			wantMemoryBytes:   480 * 1024 * 1024 * 1024,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memBytes(UnusedReservationCapacity(tt.res, tt.ignoreAllocations)); got != tt.wantMemoryBytes {
				t.Errorf("UnusedReservationCapacity() memory = %d, want %d", got, tt.wantMemoryBytes)
			}
		})
	}
}

func TestFits(t *testing.T) {
	req := func(memGiBv, cpuCores int64) map[hv1.ResourceName]resource.Quantity {
		return map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: gib(memGiBv),
			hv1.ResourceCPU:    cpu(cpuCores),
		}
	}

	tests := []struct {
		name     string
		hv       hv1.Hypervisor
		others   []v1alpha1.Reservation
		required map[hv1.ResourceName]resource.Quantity
		wantFits bool
	}{
		{
			name:     "empty host: request fits easily",
			hv:       hvWithEffCap("host", 960, 80),
			required: req(480, 40),
			wantFits: true,
		},
		{
			name:     "host fully consumed by a reservation: no room",
			hv:       hvWithEffCap("host", 480, 40),
			others:   []v1alpha1.Reservation{crSlot("blocker", "host", 480, 40)},
			required: req(480, 40),
			wantFits: false,
		},
		{
			name:     "host partially consumed, enough room left",
			hv:       hvWithEffCap("host", 960, 80),
			others:   []v1alpha1.Reservation{crSlot("blocker", "host", 480, 40)},
			required: req(480, 40),
			wantFits: true,
		},
		{
			name:     "one resource short (CPU)",
			hv:       hvWithEffCap("host", 960, 60),
			others:   []v1alpha1.Reservation{crSlot("blocker", "host", 480, 40)},
			required: req(480, 40), // mem 480 OK, but 60-40=20 CPU < 40
			wantFits: false,
		},
		{
			name: "allocation already consumed memory: no room",
			hv: func() hv1.Hypervisor {
				h := hvWithEffCap("host", 480, 40)
				h.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(100)}
				return h
			}(),
			required: req(480, 40), // 480-100=380 < 480
			wantFits: false,
		},
		{
			name:     "required resource the host does not report: does not fit",
			hv:       hvWithEffCap("host", 960, 80),
			required: map[hv1.ResourceName]resource.Quantity{hv1.ResourceName("disk"): gib(10)},
			wantFits: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, err := DescribeHostUsage(tt.hv, tt.others, Options{BlockUnplacedReservations: true})
			if err != nil {
				t.Fatalf("DescribeHostUsage() error: %v", err)
			}
			if got := usage.Fits(tt.required); got != tt.wantFits {
				t.Errorf("Fits() = %v, want %v", got, tt.wantFits)
			}
		})
	}
}

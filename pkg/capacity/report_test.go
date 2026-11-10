// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func gib(n int64) resource.Quantity {
	return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI)
}
func cpu(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

func memGiB(m ResourceMap) int64 {
	q := m[hv1.ResourceMemory]
	return q.Value() / (1024 * 1024 * 1024)
}

func hvWithCap(memGiBVal, cpuVal int64) hv1.Hypervisor {
	return hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host"},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: ResourceMap{
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
			Resources:  ResourceMap{hv1.ResourceMemory: gib(memGiBVal), hv1.ResourceCPU: cpu(cpuVal)},
		},
		Status: v1alpha1.ReservationStatus{Host: host},
	}
}

func report(hv hv1.Hypervisor, res ...v1alpha1.Reservation) HostUsageReport {
	return NewHostUsageReport(hv, res)
}

func TestNewHostUsageReport(t *testing.T) {
	t.Run("allocation subtracted into Used", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = ResourceMap{hv1.ResourceMemory: gib(512), hv1.ResourceCPU: cpu(128)}
		rep := report(hv)
		if memGiB(rep.Available) != 512 || memGiB(rep.Used) != 512 {
			t.Errorf("available=%d used=%d, want 512/512", memGiB(rep.Available), memGiB(rep.Used))
		}
	})

	t.Run("empty-host view: no reservations, Total is the free capacity", func(t *testing.T) {
		hv := hvWithCap(1024, 256)
		hv.Status.Allocation = ResourceMap{hv1.ResourceMemory: gib(512)}
		rep := NewHostUsageReport(hv, nil)
		// Used reflects the real allocation; the empty-host view simply reads Total.
		if memGiB(rep.Total) != 1024 {
			t.Errorf("Total=%d, want 1024", memGiB(rep.Total))
		}
	})

	t.Run("CR reservation bucketed under its type, subtracted from Available", func(t *testing.T) {
		rep := report(hvWithCap(1024, 256), crSlot("slot-1", "host", 512, 128))
		crReserved := rep.ReservedTotal(v1alpha1.ReservationTypeCommittedResource)
		if memGiB(rep.Available) != 512 || memGiB(crReserved) != 512 {
			t.Errorf("available=%d reserved=%d, want 512/512", memGiB(rep.Available), memGiB(crReserved))
		}
	})

	t.Run("failover reservation bucketed under failover type", func(t *testing.T) {
		fo := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "fo"},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeFailover,
				TargetHost: "host",
				Resources:  ResourceMap{hv1.ResourceMemory: gib(256), hv1.ResourceCPU: cpu(64)},
			},
			Status: v1alpha1.ReservationStatus{Host: "host"},
		}
		rep := report(hvWithCap(1024, 256), fo)
		if memGiB(rep.Available) != 768 || memGiB(rep.ReservedTotal(v1alpha1.ReservationTypeFailover)) != 256 {
			t.Errorf("available=%d failover=%d, want 768/256",
				memGiB(rep.Available), memGiB(rep.ReservedTotal(v1alpha1.ReservationTypeFailover)))
		}
		// CR-only total must not see the failover budget.
		if memGiB(rep.ReservedTotal(v1alpha1.ReservationTypeCommittedResource)) != 0 {
			t.Error("CR-only ReservedTotal should exclude failover")
		}
	})

	t.Run("reservation on another host ignored", func(t *testing.T) {
		rep := report(hvWithCap(1024, 256), crSlot("other", "other-host", 1024, 256))
		if memGiB(rep.Available) != 1024 {
			t.Errorf("available=%d, want 1024", memGiB(rep.Available))
		}
	})

	t.Run("evicted reservation (empty TargetHost) does not block", func(t *testing.T) {
		evicted := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "evicted"},
			Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource, Resources: ResourceMap{hv1.ResourceMemory: gib(480)}},
			Status:     v1alpha1.ReservationStatus{Host: "host"},
		}
		rep := report(hvWithCap(1024, 256), evicted)
		if memGiB(rep.ReservedTotal()) != 0 {
			t.Errorf("reserved=%d, want 0", memGiB(rep.ReservedTotal()))
		}
	})

	t.Run("oversubscription surfaces excess", func(t *testing.T) {
		rep := report(hvWithCap(512, 128), crSlot("big", "host", 1024, 256))
		if memGiB(rep.Available) != 0 || memGiB(rep.Oversubscribed) != 512 {
			t.Errorf("available=%d over=%d, want 0/512", memGiB(rep.Available), memGiB(rep.Oversubscribed))
		}
	})

	t.Run("per-resource fallback to raw Capacity when effective is nil", func(t *testing.T) {
		hv := hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "host"},
			Status:     hv1.HypervisorStatus{Capacity: ResourceMap{hv1.ResourceMemory: gib(512)}},
		}
		if memGiB(report(hv).Total) != 512 {
			t.Error("expected fallback to raw Capacity when EffectiveCapacity is nil")
		}
	})

	t.Run("per-resource fallback when effective reports a resource as zero", func(t *testing.T) {
		hv := hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "host"},
			Status: hv1.HypervisorStatus{
				// memory effective is zero but raw capacity has it -> fall back for memory only.
				EffectiveCapacity: ResourceMap{hv1.ResourceMemory: resource.MustParse("0"), hv1.ResourceCPU: cpu(256)},
				Capacity:          ResourceMap{hv1.ResourceMemory: gib(512), hv1.ResourceCPU: cpu(128)},
			},
		}
		rep := report(hv)
		if memGiB(rep.Total) != 512 {
			t.Errorf("memory Total=%d, want 512 (per-resource fallback)", memGiB(rep.Total))
		}
		// cpu effective is non-zero, so it is NOT overridden by raw capacity.
		if c := rep.Total[hv1.ResourceCPU]; c.Value() != 256 {
			t.Errorf("cpu Total=%d, want 256 (effective kept)", c.Value())
		}
	})
}

// TestFailoverHostBlocking documents failover-host behavior: the planner writes the full
// host capacity into Spec.Resources (see failoverhost/planning.go), so a failover-host
// reservation blocks that whole capacity regardless of lifecycle stage.
func TestFailoverHostBlocking(t *testing.T) {
	foHost := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "fh"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: "host",
			// The planner reserves the full host; Spec.Resources mirrors host capacity.
			Resources: ResourceMap{hv1.ResourceMemory: gib(1024), hv1.ResourceCPU: cpu(256)},
		},
		Status: v1alpha1.ReservationStatus{Host: "host"},
	}

	rep := report(hvWithCap(1024, 256), foHost)
	if memGiB(rep.Available) != 0 {
		t.Errorf("available=%d, want 0 (failover host reserves whole host)", memGiB(rep.Available))
	}
	if memGiB(rep.ReservedTotal(v1alpha1.ReservationTypeFailoverHost)) != 1024 {
		t.Errorf("failoverHost reserved=%d, want 1024", memGiB(rep.ReservedTotal(v1alpha1.ReservationTypeFailoverHost)))
	}
}

// TestFits checks demand against the honest Available (Total − Used − Reserved(all)).
// Which reservation types count is decided before the report is built (the capacity
// filter drops failover-host via reservationBlocks), so Fits takes no ignore list.
func TestFits(t *testing.T) {
	// 1024 GiB host with a 256 GiB CR slot -> 768 GiB available.
	rep := report(hvWithCap(1024, 256), crSlot("cr", "host", 256, 64))

	if !rep.Fits(ResourceMap{hv1.ResourceMemory: gib(768), hv1.ResourceCPU: cpu(64)}) {
		t.Error("exact fit against Available should pass")
	}
	if rep.Fits(ResourceMap{hv1.ResourceMemory: gib(769)}) {
		t.Error("over-demand should fail")
	}
	if !rep.Fits(ResourceMap{hv1.ResourceMemory: resource.Quantity{}}) {
		t.Error("zero demand imposes no constraint")
	}
	if rep.Fits(ResourceMap{"disk": gib(1)}) {
		t.Error("demanding a resource the host lacks should fail")
	}
}

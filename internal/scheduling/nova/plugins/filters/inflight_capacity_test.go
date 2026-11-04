// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestInFlightHoldsToSkip covers the per-(VM, host) in-flight skip rule directly.
func TestInFlightHoldsToSkip(t *testing.T) {
	cpu := func(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }
	inflight := func(name, vmID, host string, cpus int64) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:                v1alpha1.ReservationTypeInFlight,
				TargetHost:          host,
				Resources:           map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: cpu(cpus)},
				InFlightReservation: &v1alpha1.InFlightReservationSpec{VMID: vmID},
			},
		}
	}
	host := func(name string, active bool, vmIDs ...string) hv1.Hypervisor {
		hv := hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: name}}
		for _, id := range vmIDs {
			hv.Status.Instances = append(hv.Status.Instances, hv1.Instance{ID: id, Active: active})
		}
		return hv
	}
	tests := []struct {
		name     string
		res      []v1alpha1.Reservation
		hv       hv1.Hypervisor
		wantSkip []string
	}{
		{name: "VM not active: nothing skipped", res: []v1alpha1.Reservation{inflight("t", "vm-1", "host-a", 4)}, hv: host("host-a", false, "vm-1")},
		{name: "create landed: single hold skipped", res: []v1alpha1.Reservation{inflight("t", "vm-1", "host-a", 4)}, hv: host("host-a", true, "vm-1"), wantSkip: []string{"t"}},
		{
			name:     "same-host resize running: smaller of the two holds skipped",
			res:      []v1alpha1.Reservation{inflight("target", "vm-1", "host-a", 4), inflight("source", "vm-1", "host-a", 2)},
			hv:       host("host-a", true, "vm-1"),
			wantSkip: []string{"source"},
		},
		{
			name:     "different-host resize source still active: source hold skipped",
			res:      []v1alpha1.Reservation{inflight("target", "vm-1", "host-b", 4), inflight("source", "vm-1", "host-a", 2)},
			hv:       host("host-a", true, "vm-1"),
			wantSkip: []string{"source"},
		},
		{
			name: "different-host resize target side not active: nothing skipped",
			res:  []v1alpha1.Reservation{inflight("target", "vm-1", "host-b", 4), inflight("source", "vm-1", "host-a", 2)},
			hv:   host("host-b", false, "vm-1"),
		},
		{
			// A committed-resource (non-in-flight) reservation on the host is not
			// an in-flight hold and must never be part of the skip set.
			name: "non-in-flight reservation ignored",
			res: []v1alpha1.Reservation{{
				ObjectMeta: metav1.ObjectMeta{Name: "cr"},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-a",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: cpu(4)},
				},
			}},
			hv: host("host-a", true, "vm-1"),
		},
		{
			// The active VM is vm-1; vm-2's hold on the same host must not be
			// skipped because vm-2 is not in the host inventory.
			name:     "only the active VM's hold is skipped",
			res:      []v1alpha1.Reservation{inflight("vm1-hold", "vm-1", "host-a", 4), inflight("vm2-hold", "vm-2", "host-a", 2)},
			hv:       host("host-a", true, "vm-1"),
			wantSkip: []string{"vm1-hold"},
		},
		{
			// Two active VMs each drop their own smallest hold independently.
			name: "two active VMs each skip their smallest hold",
			res: []v1alpha1.Reservation{
				inflight("vm1-big", "vm-1", "host-a", 4), inflight("vm1-small", "vm-1", "host-a", 2),
				inflight("vm2-big", "vm-2", "host-a", 8), inflight("vm2-small", "vm-2", "host-a", 6),
			},
			hv:       host("host-a", true, "vm-1", "vm-2"),
			wantSkip: []string{"vm1-small", "vm2-small"},
		},
		{
			// A hold confirmed on the host via Status.Host (TargetHost empty) is
			// still considered for the skip rule.
			name: "hold matched via Status.Host",
			res: []v1alpha1.Reservation{{
				ObjectMeta: metav1.ObjectMeta{Name: "landed"},
				Spec: v1alpha1.ReservationSpec{
					Type:                v1alpha1.ReservationTypeInFlight,
					Resources:           map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: cpu(4)},
					InFlightReservation: &v1alpha1.InFlightReservationSpec{VMID: "vm-1"},
				},
				Status: v1alpha1.ReservationStatus{Host: "host-a"},
			}},
			hv:       host("host-a", true, "vm-1"),
			wantSkip: []string{"landed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inFlightHoldsToSkip(tt.res, tt.hv)
			if len(got) != len(tt.wantSkip) {
				t.Fatalf("skip set = %v, want %v", got, tt.wantSkip)
			}
			for _, name := range tt.wantSkip {
				if _, ok := got[reservationKey(&v1alpha1.Reservation{ObjectMeta: metav1.ObjectMeta{Name: name}})]; !ok {
					t.Errorf("expected %q in skip set %v", name, got)
				}
			}
		})
	}
}

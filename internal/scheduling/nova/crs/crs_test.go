// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crs

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// makeSlot builds a Reservation slot for testing.
func makeSlot(projectID, flavorGroup string, totalMemMiB, allocatedMemMiB int64) v1alpha1.Reservation {
	res := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-" + flavorGroup},
		Spec: v1alpha1.ReservationSpec{
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(totalMemMiB*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceGroup: flavorGroup,
			},
		},
	}
	if allocatedMemMiB > 0 {
		res.Spec.CommittedResourceReservation.Allocations = map[string]v1alpha1.CommittedResourceAllocation{
			"some-vm": {
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(allocatedMemMiB*1024*1024, resource.BinarySI),
				},
			},
		}
	}
	return res
}

// makeCR builds a CommittedResource for testing.
func makeCR(state v1alpha1.CommitmentStatus, amountMiB, usedMiB int64) v1alpha1.CommittedResource {
	cr := v1alpha1.CommittedResource{
		Spec: v1alpha1.CommittedResourceSpec{
			State:  state,
			Amount: *resource.NewQuantity(amountMiB*1024*1024, resource.BinarySI),
		},
	}
	if usedMiB > 0 {
		cr.Status.UsedResources = map[string]resource.Quantity{
			"memory": *resource.NewQuantity(usedMiB*1024*1024, resource.BinarySI),
		}
	}
	return cr
}

func TestClassifyNoHostFound(t *testing.T) {
	const (
		proj  = "project-1"
		group = "kvm_v2_hana_s"
	)
	const MiB = int64(1024 * 1024)

	emptyEval := &SlotEvaluator{
		hvFreeMemory:       map[string]int64{},
		reservationsByHost: map[string][]v1alpha1.Reservation{},
	}

	evalWithSlot := func(totalMiB, allocMiB int64) *SlotEvaluator {
		slot := makeSlot(proj, group, totalMiB, allocMiB)
		return &SlotEvaluator{
			hvFreeMemory: map[string]int64{"host-1": 16384 * MiB},
			reservationsByHost: map[string][]v1alpha1.Reservation{
				"host-1": {slot},
			},
		}
	}

	tests := []struct {
		name         string
		activeCRs    []v1alpha1.CommittedResource
		eval         *SlotEvaluator
		inputHosts   []string
		vmMemBytes   int64
		expectedCase string
	}{
		{
			name:         "no_cr: no active CRs",
			activeCRs:    nil,
			eval:         emptyEval,
			inputHosts:   nil,
			vmMemBytes:   4096 * MiB,
			expectedCase: "no_cr",
		},
		{
			name: "cr_exhausted: CRs fully occupied (used == capacity)",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 8192),
			},
			eval:         emptyEval,
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "cr_exhausted",
		},
		{
			name: "cr_exhausted: CRs fully occupied (used > capacity)",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 10000),
			},
			eval:         emptyEval,
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "cr_exhausted",
		},
		{
			name: "cr_exhausted: multiple CRs, total used >= total capacity",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 4096, 4096),
				makeCR(v1alpha1.CommitmentStatusGuaranteed, 4096, 4096),
			},
			eval:         emptyEval,
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "cr_exhausted",
		},
		{
			name: "slot_exhausted: CRs have capacity but slot fully allocated",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
			},
			eval:         evalWithSlot(8192, 8192), // slotRemaining=0 → skipped
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "slot_exhausted",
		},
		{
			name: "slot_exhausted: CRs have capacity, no slots at all",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			eval:         emptyEval,
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "slot_exhausted",
		},
		{
			name: "slot_blocked: free slot exists on input host",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
			},
			eval:         evalWithSlot(8192, 4096), // 4096 MiB remaining; 16384-8192+4096=12288 >= 4096
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "slot_blocked",
		},
		{
			name: "slot_blocked: overfill — slot smaller than VM is still usable",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
			},
			eval:         evalWithSlot(8192, 6144), // 2048 MiB remaining; 16384-8192+2048=10240 >= 4096
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "slot_blocked",
		},
		{
			name: "slot_exhausted: slots for other project ignored",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			eval: &SlotEvaluator{
				hvFreeMemory: map[string]int64{"host-1": 16384 * MiB},
				reservationsByHost: map[string][]v1alpha1.Reservation{
					"host-1": {makeSlot("other-project", group, 8192, 0)},
				},
			},
			inputHosts:   []string{"host-1"},
			vmMemBytes:   4096 * MiB,
			expectedCase: "slot_exhausted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyNoHostFound(tt.activeCRs, tt.eval, tt.inputHosts, proj, group, tt.vmMemBytes)
			if got != tt.expectedCase {
				t.Errorf("ClassifyNoHostFound() = %q, want %q", got, tt.expectedCase)
			}
		})
	}
}

func TestReservationRemainingMemory(t *testing.T) {
	tests := []struct {
		name        string
		totalMemMiB int64
		usedMemMiB  int64
		wantBytes   int64
	}{
		{"empty slot", 8192, 0, 8192 * 1024 * 1024},
		{"partially used", 8192, 4096, 4096 * 1024 * 1024},
		{"fully used", 8192, 8192, 0},
		{"over-allocated (clamped to zero)", 4096, 8192, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := makeSlot("proj", "group", tt.totalMemMiB, tt.usedMemMiB)
			got := ReservationRemainingMemory(res)
			if got != tt.wantBytes {
				t.Errorf("ReservationRemainingMemory() = %d, want %d", got, tt.wantBytes)
			}
		})
	}
}

func TestPickSlot(t *testing.T) {
	// vmMemBytes for a 4096 MiB flavor.
	const vmMemBytes = int64(4096) * 1024 * 1024

	makePickSlot := func(name string, totalMemMiB, totalCPU, usedMemMiB, usedCPU int64) v1alpha1.Reservation {
		var allocs map[string]v1alpha1.CommittedResourceAllocation
		if usedMemMiB > 0 || usedCPU > 0 {
			allocs = map[string]v1alpha1.CommittedResourceAllocation{
				"vm-existing": {
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: *resource.NewQuantity(usedMemMiB*1024*1024, resource.BinarySI),
						hv1.ResourceCPU:    *resource.NewQuantity(usedCPU, resource.DecimalSI),
					},
				},
			}
		}
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(totalMemMiB*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(totalCPU, resource.DecimalSI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Allocations: allocs,
				},
			},
		}
	}

	tests := []struct {
		name       string
		candidates []v1alpha1.Reservation
		want       string
	}{
		{
			name:       "no candidates",
			candidates: nil,
			want:       "",
		},
		{
			name:       "single slot fits fully",
			candidates: []v1alpha1.Reservation{makePickSlot("a", 8192, 8, 0, 0)},
			want:       "a",
		},
		{
			name:       "slot with zero remaining memory excluded",
			candidates: []v1alpha1.Reservation{makePickSlot("a", 8192, 8, 8192, 0)},
			want:       "",
		},
		{
			name: "picks slot with least remaining memory when both cover fully",
			candidates: []v1alpha1.Reservation{
				makePickSlot("large", 8192, 8, 0, 0), // 8192 MiB remaining
				makePickSlot("small", 6144, 8, 0, 0), // 6144 MiB remaining
			},
			want: "small",
		},
		{
			name: "name tiebreak when coverage and remaining memory are equal",
			candidates: []v1alpha1.Reservation{
				makePickSlot("slot-b", 6144, 4, 0, 0),
				makePickSlot("slot-a", 6144, 4, 0, 0),
			},
			want: "slot-a",
		},
		{
			name: "missing resource keys treated as zero remaining",
			candidates: []v1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "empty-res"},
					Spec: v1alpha1.ReservationSpec{
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
					},
				},
			},
			want: "",
		},
		{
			name:       "partially used slot still usable",
			candidates: []v1alpha1.Reservation{makePickSlot("partial", 8192, 8, 2048, 2)}, // 6144 MiB remaining
			want:       "partial",
		},
		{
			name:       "overfill: slot smaller than VM is usable",
			candidates: []v1alpha1.Reservation{makePickSlot("a", 4096, 8, 2048, 0)}, // 2048 MiB remaining < vmMemBytes
			want:       "a",
		},
		{
			name: "overfill: full coverage preferred over partial",
			candidates: []v1alpha1.Reservation{
				makePickSlot("partial", 4096, 8, 2048, 0), // 2048 MiB remaining, coverage=2048
				makePickSlot("full", 6144, 8, 0, 0),       // 6144 MiB remaining, coverage=4096 (full)
			},
			want: "full",
		},
		{
			name: "overfill: highest partial coverage preferred",
			candidates: []v1alpha1.Reservation{
				makePickSlot("low", 4096, 8, 2048, 0),  // 2048 MiB remaining, coverage=2048
				makePickSlot("high", 4096, 8, 1024, 0), // 3072 MiB remaining, coverage=3072
			},
			want: "high",
		},
		{
			name:       "CPU exhausted slot is still usable (overfill applies to CPU too)",
			candidates: []v1alpha1.Reservation{makePickSlot("cpu-full", 8192, 2, 0, 2)}, // remCPU=0 but remMem>0
			want:       "cpu-full",
		},
		{
			name: "name tiebreak when memory equal (CPU no longer a tiebreak criterion)",
			candidates: []v1alpha1.Reservation{
				makePickSlot("less-cpu", 6144, 4, 0, 0),
				makePickSlot("more-cpu", 6144, 8, 0, 0),
			},
			want: "less-cpu", // wins by name, not by CPU
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PickSlot(tt.candidates, vmMemBytes)
			if got != tt.want {
				t.Errorf("PickSlot() = %q, want %q", got, tt.want)
			}
		})
	}
}

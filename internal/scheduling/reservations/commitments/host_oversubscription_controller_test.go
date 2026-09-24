// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

func hvReadyCondition() metav1.Condition {
	return metav1.Condition{
		Type:               hv1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             hv1.ConditionReasonReadyReady,
		LastTransitionTime: metav1.Now(),
	}
}

func TestComputeViolations(t *testing.T) {
	tests := []struct {
		name          string
		hv            hv1.Hypervisor
		slots         []v1alpha1.Reservation
		wantNil       bool
		wantViolation bool
		wantExcessGiB int64
	}{
		{
			name:    "nil capacity returns nil",
			hv:      hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}},
			wantNil: true,
		},
		{
			name: "slot within capacity: no violation",
			hv: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: testGiB(1024),
					},
				},
			},
			slots: []v1alpha1.Reservation{{
				Spec: v1alpha1.ReservationSpec{
					Type:                         v1alpha1.ReservationTypeCommittedResource,
					TargetHost:                   "host-1",
					Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
				},
				Status: v1alpha1.ReservationStatus{Host: "host-1"},
			}},
		},
		{
			name: "slot exceeds capacity: violation reported",
			hv: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host-1"},
				Status: hv1.HypervisorStatus{
					EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: testGiB(512),
					},
				},
			},
			slots: []v1alpha1.Reservation{{
				Spec: v1alpha1.ReservationSpec{
					Type:                         v1alpha1.ReservationTypeCommittedResource,
					TargetHost:                   "host-1",
					Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(768)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
				},
				Status: v1alpha1.ReservationStatus{Host: "host-1"},
			}},
			wantViolation: true,
			wantExcessGiB: 256,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := computeViolations(tt.slots, tt.hv)
			if tt.wantNil {
				if v != nil {
					t.Errorf("expected nil, got %v", v)
				}
				return
			}
			if tt.wantViolation && len(v) == 0 {
				t.Fatal("expected violation, got none")
			}
			if !tt.wantViolation && len(v) != 0 {
				t.Errorf("expected no violation, got %v", v)
			}
			if tt.wantExcessGiB != 0 {
				excess := v[hv1.ResourceMemory]
				if excess.Cmp(testGiB(tt.wantExcessGiB)) != 0 {
					t.Errorf("expected excess %d GiB, got %s", tt.wantExcessGiB, excess.String())
				}
			}
		})
	}
}

func TestSelectEvictionTarget(t *testing.T) {
	unallocated := func(name string, memGiB int64) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:                         v1alpha1.ReservationTypeCommittedResource,
				TargetHost:                   "host-1",
				Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(memGiB)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
			},
		}
	}
	allocated := func(name string, totalGiB, usedGiB int64, vmID string) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: "host-1",
				Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(totalGiB)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Allocations: map[string]v1alpha1.CommittedResourceAllocation{
						vmID: {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(usedGiB)}},
					},
				},
			},
			Status: v1alpha1.ReservationStatus{
				Host: "host-1",
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
					Allocations: map[string]string{vmID: "host-1"},
				},
			},
		}
	}

	violations := map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(128)}

	tests := []struct {
		name     string
		slots    []v1alpha1.Reservation
		wantName string // empty = expect nil
	}{
		{
			name: "prefers smallest unallocated over any allocated",
			slots: []v1alpha1.Reservation{
				allocated("big-alloc", 512, 256, "vm-1"),
				unallocated("large", 512),
				unallocated("small", 128),
			},
			wantName: "small",
		},
		{
			name: "3 allocated slots: selects most-idle (validates sort ordering)",
			slots: []v1alpha1.Reservation{
				allocated("mostly-used", 512, 480, "vm-1"), // ~6% unused
				allocated("half-used", 512, 256, "vm-2"),   // ~50% unused
				allocated("idle", 512, 10, "vm-3"),         // ~98% unused
			},
			wantName: "idle",
		},
		{
			name: "skips non-CR reservation types",
			slots: []v1alpha1.Reservation{{
				ObjectMeta: metav1.ObjectMeta{Name: "failover"},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeFailover,
					TargetHost: "host-1",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
				},
			}},
			wantName: "",
		},
		{
			name: "skips already-evicted slots (TargetHost cleared)",
			slots: []v1alpha1.Reservation{{
				ObjectMeta: metav1.ObjectMeta{Name: "evicted"},
				Spec: v1alpha1.ReservationSpec{
					Type:                         v1alpha1.ReservationTypeCommittedResource,
					TargetHost:                   "",
					Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
				},
			}},
			wantName: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := selectEvictionTarget(context.Background(), tt.slots, violations)
			if tt.wantName == "" {
				if target != nil {
					t.Errorf("expected nil target, got %q", target.Name)
				}
				return
			}
			if target == nil {
				t.Fatal("expected a target, got nil")
			}
			if target.Name != tt.wantName {
				t.Errorf("expected %q, got %q", tt.wantName, target.Name)
			}
		})
	}
}

func TestCheckOversubscription_StartsGracePeriod(t *testing.T) {
	scheme := newCRTestScheme(t)
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host-1", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(1024)},
			Conditions:        []metav1.Condition{hvReadyCondition()},
		},
	}
	makeSlot := func(name string) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:                         v1alpha1.ReservationTypeCommittedResource,
				TargetHost:                   "host-1",
				Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
			},
			Status: v1alpha1.ReservationStatus{Host: "host-1"},
		}
	}
	// 3 × 512 GiB slots on a 1024 GiB host → over-subscribed by 512 GiB
	slots := []v1alpha1.Reservation{makeSlot("slot-1"), makeSlot("slot-2"), makeSlot("slot-3")}
	k8sClient := newCRTestClient(scheme, &slots[0], &slots[1], &slots[2])
	monitor := NewReservationControllerMonitor()
	controller := &HostOversubscriptionController{
		Client:  k8sClient,
		Monitor: &monitor,
		Conf:    ReservationControllerConfig{EnableOversubscriptionReservationEviction: true},
	}

	requeue, err := controller.checkOversubscription(context.Background(), "host-1", slots, hv, 2*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requeue == 0 {
		t.Error("expected non-zero requeue on first violation (grace period)")
	}
	if controller.firstSeen["host-1"].IsZero() {
		t.Error("expected firstSeen recorded after first violation")
	}
	// No eviction during grace period — all slots must still be placed.
	for _, name := range []string{"slot-1", "slot-2", "slot-3"} {
		var r v1alpha1.Reservation
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: name}, &r); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if r.Spec.TargetHost == "" {
			t.Errorf("%s: TargetHost cleared during grace period", name)
		}
	}
}

func TestCheckOversubscription_EvictsAfterGracePeriod(t *testing.T) {
	scheme := newCRTestScheme(t)
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host-1", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(1024)},
			Conditions:        []metav1.Condition{hvReadyCondition()},
		},
	}
	makeSlot := func(name string) v1alpha1.Reservation {
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Type:                         v1alpha1.ReservationTypeCommittedResource,
				TargetHost:                   "host-1",
				Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
			},
			Status: v1alpha1.ReservationStatus{Host: "host-1"},
		}
	}
	slots := []v1alpha1.Reservation{makeSlot("slot-1"), makeSlot("slot-2"), makeSlot("slot-3")}
	k8sClient := newCRTestClient(scheme, &slots[0], &slots[1], &slots[2])
	monitor := NewReservationControllerMonitor()
	controller := &HostOversubscriptionController{
		Client:    k8sClient,
		Monitor:   &monitor,
		Conf:      ReservationControllerConfig{EnableOversubscriptionReservationEviction: true},
		firstSeen: map[string]time.Time{"host-1": time.Now().Add(-3 * time.Minute)},
	}

	_, err := controller.checkOversubscription(context.Background(), "host-1", slots, hv, 2*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var evicted []string
	for _, name := range []string{"slot-1", "slot-2", "slot-3"} {
		var r v1alpha1.Reservation
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: name}, &r); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if r.Spec.TargetHost == "" {
			evicted = append(evicted, name)
		}
	}
	if len(evicted) != 1 {
		t.Errorf("expected exactly 1 slot evicted, got %v", evicted)
	}
}

func TestCheckOversubscription_SkipsWhenHVNotReady(t *testing.T) {
	scheme := newCRTestScheme(t)
	// HV has capacity data but no Ready condition.
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host-1"},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
		},
	}
	// Slot is oversubscribed relative to HV capacity — should NOT be evicted.
	slot := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
		Spec: v1alpha1.ReservationSpec{
			Type:                         v1alpha1.ReservationTypeCommittedResource,
			TargetHost:                   "host-1",
			Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(768)},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
		},
	}
	k8sClient := newCRTestClient(scheme, &slot)
	monitor := NewReservationControllerMonitor()
	controller := &HostOversubscriptionController{
		Client:  k8sClient,
		Monitor: &monitor,
		Conf:    ReservationControllerConfig{EnableOversubscriptionReservationEviction: true},
	}

	requeue, err := controller.checkOversubscription(context.Background(), "host-1", []v1alpha1.Reservation{slot}, hv, 2*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requeue != 0 {
		t.Errorf("expected 0 requeue when HV not ready, got %v", requeue)
	}
	if !controller.firstSeen["host-1"].IsZero() {
		t.Error("expected firstSeen not set when HV not ready")
	}
}

func TestCheckOversubscription_NoViolation(t *testing.T) {
	scheme := newCRTestScheme(t)
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "host-1", Labels: map[string]string{"topology.kubernetes.io/zone": "az1"}},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(1024)},
			Conditions:        []metav1.Condition{hvReadyCondition()},
		},
	}
	slot := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
		Spec: v1alpha1.ReservationSpec{
			Type:                         v1alpha1.ReservationTypeCommittedResource,
			TargetHost:                   "host-1",
			Resources:                    map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
		},
	}
	k8sClient := newCRTestClient(scheme, &slot)
	monitor := NewReservationControllerMonitor()
	controller := &HostOversubscriptionController{
		Client:    k8sClient,
		Monitor:   &monitor,
		firstSeen: map[string]time.Time{"host-1": time.Now().Add(-5 * time.Minute)},
	}

	requeue, err := controller.checkOversubscription(context.Background(), "host-1", []v1alpha1.Reservation{slot}, hv, 2*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requeue != 0 {
		t.Errorf("expected 0 requeue when no violation, got %v", requeue)
	}
	if _, still := controller.firstSeen["host-1"]; still {
		t.Error("expected firstSeen cleared after violation resolved")
	}
}

func TestHostOversubscriptionController_RateLimit(t *testing.T) {
	controller := &HostOversubscriptionController{}
	minInterval := 30 * time.Second

	if limited, _ := controller.isRateLimited("host-1", minInterval); limited {
		t.Error("expected not rate-limited before first check")
	}

	controller.recordCheck("host-1")

	limited, remaining := controller.isRateLimited("host-1", minInterval)
	if !limited {
		t.Error("expected rate-limited immediately after recordCheck")
	}
	if remaining <= 0 || remaining > minInterval {
		t.Errorf("remaining out of expected range (0, %v]: %v", minInterval, remaining)
	}

	controller.lastCheck["host-1"] = time.Time{}
	if limited, _ := controller.isRateLimited("host-1", minInterval); limited {
		t.Error("expected not rate-limited after zeroing lastCheck")
	}
}

func TestEvictReservation_ClearsAllocations(t *testing.T) {
	scheme := newCRTestScheme(t)
	slot := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-1",
			Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(512)},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-1": {Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(256)}},
				},
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]string{"vm-1": "host-1"},
			},
		},
	}

	k8sClient := newCRTestClient(scheme, slot)
	freed, err := evictReservation(context.Background(), k8sClient, slot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	freedMem := freed[hv1.ResourceMemory]
	if freedMem.Cmp(testGiB(512)) != 0 {
		t.Errorf("expected freed 512 GiB, got %s", freedMem.String())
	}

	var updated v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "slot-1"}, &updated); err != nil {
		t.Fatalf("get updated reservation: %v", err)
	}
	if updated.Spec.TargetHost != "" {
		t.Errorf("expected TargetHost cleared, got %q", updated.Spec.TargetHost)
	}
	if len(updated.Spec.CommittedResourceReservation.Allocations) != 0 {
		t.Errorf("expected Spec.Allocations cleared, got %v", updated.Spec.CommittedResourceReservation.Allocations)
	}
}

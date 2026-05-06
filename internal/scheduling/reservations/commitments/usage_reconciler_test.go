// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func i64ptr(i int64) *int64 { return &i }

// newUsageReconciler builds a minimal UsageReconciler for unit tests.
func newUsageReconciler(k8sClient client.Client, cooldown time.Duration) *UsageReconciler {
	return &UsageReconciler{
		Client:  k8sClient,
		Conf:    UsageReconcilerConfig{CooldownInterval: metav1.Duration{Duration: cooldown}},
		Monitor: NewUsageReconcilerMonitor(),
	}
}

// ============================================================================
// acceptedGenerationPredicate
// ============================================================================

func TestAcceptedGenerationPredicate_Update(t *testing.T) {
	readyCond := metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: 1,
		Reason:             v1alpha1.CommittedResourceReasonAccepted,
	}

	tests := []struct {
		name string
		old  *v1alpha1.CommittedResource
		new  *v1alpha1.CommittedResource
		want bool
	}{
		{
			name: "generation changed: spec update ignored by this predicate",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 2}},
			want: false,
		},
		{
			name: "no Ready condition",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			want: false,
		},
		{
			name: "Ready=False",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new: &v1alpha1.CommittedResource{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: v1alpha1.CommittedResourceStatus{
					Conditions: []metav1.Condition{{
						Type:               v1alpha1.CommittedResourceConditionReady,
						Status:             metav1.ConditionFalse,
						ObservedGeneration: 1,
						Reason:             v1alpha1.CommittedResourceReasonReserving,
					}},
				},
			},
			want: false,
		},
		{
			name: "Ready=True but ObservedGeneration lags metadata.generation",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 2}},
			new: &v1alpha1.CommittedResource{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status: v1alpha1.CommittedResourceStatus{
					Conditions: []metav1.Condition{{
						Type:               v1alpha1.CommittedResourceConditionReady,
						Status:             metav1.ConditionTrue,
						ObservedGeneration: 1, // lags behind generation=2
						Reason:             v1alpha1.CommittedResourceReasonAccepted,
					}},
				},
			},
			want: false,
		},
		{
			name: "usage already current: UsageObsGen equals Ready.ObservedGeneration",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new: &v1alpha1.CommittedResource{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: v1alpha1.CommittedResourceStatus{
					Conditions:              []metav1.Condition{readyCond},
					UsageObservedGeneration: i64ptr(1),
				},
			},
			want: false,
		},
		{
			name: "UsageObsGen nil: fires (first usage reconcile needed after acceptance)",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new: &v1alpha1.CommittedResource{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: v1alpha1.CommittedResourceStatus{
					Conditions:              []metav1.Condition{readyCond},
					UsageObservedGeneration: nil,
				},
			},
			want: true,
		},
		{
			name: "UsageObsGen lags: fires (retrigger after cache-race miss)",
			old:  &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Generation: 1}},
			new: &v1alpha1.CommittedResource{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: v1alpha1.CommittedResourceStatus{
					Conditions:              []metav1.Condition{readyCond},
					UsageObservedGeneration: i64ptr(0), // lags behind Ready.ObservedGeneration=1
				},
			},
			want: true,
		},
	}

	p := acceptedGenerationPredicate{log: logr.Discard()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}
			if got := p.Update(ev); got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// UsageReconciler.Reconcile gate logic
// ============================================================================

func TestUsageReconciler_Reconcile_Gates(t *testing.T) {
	scheme := newCRTestScheme(t)
	const cooldown = 5 * time.Minute

	t.Run("CR not found: returns nil without requeue", func(t *testing.T) {
		r := newUsageReconciler(newCRTestClient(scheme), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq("nonexistent"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter != 0 {
			t.Errorf("RequeueAfter = %v, want 0", result.RequeueAfter)
		}
	})

	t.Run("non-active state without stale data: no patch, no requeue", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusPlanned)
		r := newUsageReconciler(newCRTestClient(scheme, cr), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter != 0 {
			t.Errorf("RequeueAfter = %v, want 0", result.RequeueAfter)
		}
	})

	t.Run("non-active state with stale data: clears AssignedInstances and timestamps", func(t *testing.T) {
		now := metav1.Now()
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusExpired)
		cr.Status.AssignedInstances = []string{"vm-1", "vm-2"}
		cr.Status.LastUsageReconcileAt = &now

		k8sClient := newCRTestClient(scheme, cr)
		r := newUsageReconciler(k8sClient, cooldown)
		_, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updated v1alpha1.CommittedResource
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &updated); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		if len(updated.Status.AssignedInstances) != 0 {
			t.Errorf("AssignedInstances = %v, want nil", updated.Status.AssignedInstances)
		}
		if updated.Status.LastUsageReconcileAt != nil {
			t.Errorf("LastUsageReconcileAt = %v, want nil", updated.Status.LastUsageReconcileAt)
		}
	})

	t.Run("Ready condition absent: skips without requeue", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		r := newUsageReconciler(newCRTestClient(scheme, cr), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter != 0 {
			t.Errorf("RequeueAfter = %v, want 0 (no requeue until predicate fires)", result.RequeueAfter)
		}
	})

	t.Run("Ready=True but ObsGen behind metadata.generation: skips without requeue", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		cr.Generation = 2
		cr.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1, // lags behind generation=2
			Reason:             v1alpha1.CommittedResourceReasonAccepted,
		}}
		r := newUsageReconciler(newCRTestClient(scheme, cr), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter != 0 {
			t.Errorf("RequeueAfter = %v, want 0", result.RequeueAfter)
		}
	})

	t.Run("cooldown active: recent reconcile returns RequeueAfter near cooldown boundary", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		cr.Generation = 1
		now := metav1.Now()
		cr.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
			Reason:             v1alpha1.CommittedResourceReasonAccepted,
		}}
		cr.Status.LastUsageReconcileAt = &now
		cr.Status.UsageObservedGeneration = i64ptr(1) // up to date → generationAdvanced=false

		r := newUsageReconciler(newCRTestClient(scheme, cr), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter <= 0 || result.RequeueAfter > cooldown {
			t.Errorf("RequeueAfter = %v, want (0, %v]", result.RequeueAfter, cooldown)
		}
	})

	t.Run("generation advanced bypasses cooldown: runs past cooldown to buildCommitmentCapacityMap", func(t *testing.T) {
		// UsageObsGen=nil means generationAdvanced=true, bypassing cooldown.
		// The CR has no AcceptedSpec/AcceptedAmount so buildCommitmentCapacityMap returns
		// empty, triggering the "no active commitments" early-exit with RequeueAfter=cooldown.
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		cr.Generation = 1
		cr.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
			Reason:             v1alpha1.CommittedResourceReasonAccepted,
		}}
		// UsageObservedGeneration intentionally left nil

		r := newUsageReconciler(newCRTestClient(scheme, cr, newTestFlavorKnowledge()), cooldown)
		result, err := r.Reconcile(context.Background(), reconcileReq(cr.Name))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RequeueAfter != cooldown {
			t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, cooldown)
		}
	})
}

// ============================================================================
// hypervisorToCommittedResources mapper
// ============================================================================

func TestUsageReconciler_HypervisorToCommittedResources(t *testing.T) {
	scheme := newCRTestScheme(t)
	const cooldown = 5 * time.Minute

	t.Run("no reservations: empty result", func(t *testing.T) {
		r := newUsageReconciler(newCRTestClient(scheme), cooldown)
		hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
		if reqs := r.hypervisorToCommittedResources(context.Background(), hv); len(reqs) != 0 {
			t.Errorf("got %d requests, want 0", len(reqs))
		}
	})

	t.Run("reservation on different host: no matching project, empty result", func(t *testing.T) {
		res := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "res-1",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeCommittedResource,
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-a",
				},
			},
			Status: v1alpha1.ReservationStatus{Host: "host-2"}, // different host
		}
		r := newUsageReconciler(newCRTestClient(scheme, res), cooldown)
		hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
		if reqs := r.hypervisorToCommittedResources(context.Background(), hv); len(reqs) != 0 {
			t.Errorf("got %d requests, want 0", len(reqs))
		}
	})

	t.Run("reservation on host with matching CR: returns request for that CR", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		res := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "res-1",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeCommittedResource,
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:      cr.Spec.ProjectID,
					CommitmentUUID: cr.Spec.CommitmentUUID,
				},
			},
			Status: v1alpha1.ReservationStatus{Host: "host-1"},
		}

		r := newUsageReconciler(newCRTestClient(scheme, cr, res), cooldown)
		hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
		reqs := r.hypervisorToCommittedResources(context.Background(), hv)
		if len(reqs) != 1 {
			t.Fatalf("got %d requests, want 1", len(reqs))
		}
		if reqs[0].NamespacedName.Name != cr.Name {
			t.Errorf("request name = %q, want %q", reqs[0].NamespacedName.Name, cr.Name)
		}
	})

	t.Run("two CRs share a host: both enqueued", func(t *testing.T) {
		cr1 := newTestCommittedResource("cr-1", v1alpha1.CommitmentStatusConfirmed)
		cr1.Spec.CommitmentUUID = "uuid-cr1"

		cr2 := newTestCommittedResource("cr-2", v1alpha1.CommitmentStatusConfirmed)
		cr2.Spec.CommitmentUUID = "uuid-cr2"
		// Same project so a single reservation entry covers both CRs.

		res := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "res-1",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeCommittedResource,
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:      cr1.Spec.ProjectID,
					CommitmentUUID: cr1.Spec.CommitmentUUID,
				},
			},
			Status: v1alpha1.ReservationStatus{Host: "host-1"},
		}

		r := newUsageReconciler(newCRTestClient(scheme, cr1, cr2, res), cooldown)
		hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
		reqs := r.hypervisorToCommittedResources(context.Background(), hv)
		if len(reqs) != 2 {
			t.Errorf("got %d requests, want 2", len(reqs))
		}
	})
}

// ============================================================================
// writeUsageStatus
// ============================================================================

func TestUsageReconciler_WriteUsageStatus(t *testing.T) {
	scheme := newCRTestScheme(t)

	t.Run("UUID not in index: returns nil without error", func(t *testing.T) {
		r := newUsageReconciler(newCRTestClient(scheme), time.Minute)
		state := &CommitmentStateWithUsage{
			CommitmentState: CommitmentState{
				CommitmentUUID:   "no-such-uuid",
				TotalMemoryBytes: 4 * 1024 * 1024 * 1024,
			},
			RemainingMemoryBytes: 2 * 1024 * 1024 * 1024,
		}
		if err := r.writeUsageStatus(context.Background(), state, metav1.Now()); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("UUID found: patches AssignedInstances, UsedResources, and generation", func(t *testing.T) {
		cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
		cr.Generation = 1
		k8sClient := newCRTestClient(scheme, cr)
		r := newUsageReconciler(k8sClient, time.Minute)

		now := metav1.Now()
		state := &CommitmentStateWithUsage{
			CommitmentState: CommitmentState{
				CommitmentUUID:   cr.Spec.CommitmentUUID,
				TotalMemoryBytes: 4 * 1024 * 1024 * 1024,
			},
			RemainingMemoryBytes: 2 * 1024 * 1024 * 1024, // 2 GiB used
			UsedVCPUs:            4,
			AssignedInstances:    []string{"vm-1", "vm-2"},
		}

		if err := r.writeUsageStatus(context.Background(), state, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var updated v1alpha1.CommittedResource
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &updated); err != nil {
			t.Fatalf("get CR: %v", err)
		}

		if len(updated.Status.AssignedInstances) != 2 {
			t.Errorf("AssignedInstances len = %d, want 2", len(updated.Status.AssignedInstances))
		}
		if updated.Status.LastUsageReconcileAt == nil {
			t.Errorf("LastUsageReconcileAt not set")
		}
		if updated.Status.UsageObservedGeneration == nil || *updated.Status.UsageObservedGeneration != 1 {
			t.Errorf("UsageObservedGeneration = %v, want 1", updated.Status.UsageObservedGeneration)
		}
		if _, ok := updated.Status.UsedResources["memory"]; !ok {
			t.Errorf("UsedResources[memory] not set")
		}
		if _, ok := updated.Status.UsedResources["cpu"]; !ok {
			t.Errorf("UsedResources[cpu] not set")
		}
	})
}

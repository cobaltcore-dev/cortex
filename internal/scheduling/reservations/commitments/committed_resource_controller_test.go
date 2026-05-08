// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ============================================================================
// Helpers
// ============================================================================

// newTestCommittedResource returns a CommittedResource with sensible defaults.
func newTestCommittedResource(name string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "test-uuid-1234",
			FlavorGroupName:  "test-group",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           resource.MustParse("4Gi"),
			AvailabilityZone: "test-az",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
			State:            state,
		},
	}
}

// newTestFlavorKnowledge returns a Knowledge CRD with a single 4 GiB flavor so
// a 4 GiB commitment produces exactly one slot.
func newTestFlavorKnowledge() *v1alpha1.Knowledge {
	raw, err := json.Marshal(map[string]any{
		"features": []map[string]any{
			{
				"name": "test-group",
				"flavors": []map[string]any{
					{
						"name":       "test-flavor",
						"memoryMB":   4096,
						"vcpus":      2,
						"extraSpecs": map[string]string{},
					},
				},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw:       runtime.RawExtension{Raw: raw},
			RawLength: 1,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "Ready",
				},
			},
		},
	}
}

func newCRTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

func newCRTestClient(scheme *runtime.Scheme, objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.CommittedResource{}, &v1alpha1.Reservation{}).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByCommitmentUUID, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.CommitmentUUID == "" {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, idxCommittedResourceByUUID, func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.CommitmentUUID == "" {
				return nil
			}
			return []string{cr.Spec.CommitmentUUID}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, idxCommittedResourceByProjectID, func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.ProjectID == "" {
				return nil
			}
			return []string{cr.Spec.ProjectID}
		}).
		Build()
}

func reconcileReq(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// assertCondition checks the Ready condition status and reason on a CommittedResource.
func assertCondition(t *testing.T, k8sClient client.Client, crName string, expectedStatus metav1.ConditionStatus, expectedReason string) {
	t.Helper()
	var cr v1alpha1.CommittedResource
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: crName}, &cr); err != nil {
		t.Fatalf("failed to get CommittedResource %s: %v", crName, err)
	}
	cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil {
		t.Errorf("Ready condition not set on %s", crName)
		return
	}
	if cond.Status != expectedStatus {
		t.Errorf("%s: expected Ready=%s, got %s", crName, expectedStatus, cond.Status)
	}
	if cond.Reason != expectedReason {
		t.Errorf("%s: expected Reason=%s, got %s", crName, expectedReason, cond.Reason)
	}
}

// countChildReservations counts Reservation CRDs owned by the given CommitmentUUID,
// using the same identity predicate as the controller.
func countChildReservations(t *testing.T, k8sClient client.Client, commitmentUUID string) int {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("failed to list reservations: %v", err)
	}
	count := 0
	for _, r := range list.Items {
		if r.Spec.CommittedResourceReservation != nil &&
			r.Spec.CommittedResourceReservation.CommitmentUUID == commitmentUUID {
			count++
		}
	}
	return count
}

// setChildReservationsReady simulates the reservation controller by marking all child
// Reservations for the given commitmentUUID as Ready=True and echoing ParentGeneration
// into ObservedParentGeneration (matching what echoParentGeneration does in production).
func setChildReservationsReady(t *testing.T, k8sClient client.Client, commitmentUUID string) {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	for i := range list.Items {
		res := &list.Items[i]
		if res.Spec.CommittedResourceReservation == nil ||
			res.Spec.CommittedResourceReservation.CommitmentUUID != commitmentUUID {
			continue
		}
		res.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ReservationActive",
			LastTransitionTime: metav1.Now(),
		}}
		if res.Status.CommittedResourceReservation == nil {
			res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
		}
		res.Status.CommittedResourceReservation.ObservedParentGeneration = res.Spec.CommittedResourceReservation.ParentGeneration
		if err := k8sClient.Status().Update(context.Background(), res); err != nil {
			t.Fatalf("set reservation Ready=True: %v", err)
		}
	}
}

// ============================================================================
// Tests: per-state reconcile paths
// ============================================================================

func TestCommittedResourceController_Reconcile(t *testing.T) {
	tests := []struct {
		name           string
		state          v1alpha1.CommitmentStatus
		expectedStatus metav1.ConditionStatus
		expectedReason string
		expectedSlots  int
		needsKnowledge bool
	}{
		{
			name:           "planned: no Reservations created, Ready=False/Planned",
			state:          v1alpha1.CommitmentStatusPlanned,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Planned",
			expectedSlots:  0,
		},
		{
			name:           "pending: Reservations created, Ready=True",
			state:          v1alpha1.CommitmentStatusPending,
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "Accepted",
			expectedSlots:  1,
			needsKnowledge: true,
		},
		{
			name:           "guaranteed: Reservations created, Ready=True",
			state:          v1alpha1.CommitmentStatusGuaranteed,
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "Accepted",
			expectedSlots:  1,
			needsKnowledge: true,
		},
		{
			name:           "confirmed: Reservations created, Ready=True",
			state:          v1alpha1.CommitmentStatusConfirmed,
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "Accepted",
			expectedSlots:  1,
			needsKnowledge: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			cr := newTestCommittedResource("test-cr", tt.state)
			objects := []client.Object{cr}
			if tt.needsKnowledge {
				objects = append(objects, newTestFlavorKnowledge())
			}
			k8sClient := newCRTestClient(scheme, objects...)
			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

			// First reconcile: creates Reservation CRDs; if slots are expected, controller
			// waits for the reservation controller to set Ready=True before accepting.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("reconcile 1: %v", err)
			}

			if tt.expectedSlots > 0 {
				// Simulate reservation controller: mark all child reservations as Ready=True.
				setChildReservationsReady(t, k8sClient, cr.Spec.CommitmentUUID)
				// Second reconcile: sees all Ready=True and accepts.
				if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
					t.Fatalf("reconcile 2: %v", err)
				}
			}

			assertCondition(t, k8sClient, cr.Name, tt.expectedStatus, tt.expectedReason)
			if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != tt.expectedSlots {
				t.Errorf("expected %d child reservations, got %d", tt.expectedSlots, got)
			}

			if tt.expectedSlots > 0 {
				var updated v1alpha1.CommittedResource
				if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &updated); err != nil {
					t.Fatalf("get CR: %v", err)
				}
				if updated.Status.AcceptedSpec == nil {
					t.Errorf("expected AcceptedSpec to be set on acceptance")
				} else if updated.Status.AcceptedSpec.AvailabilityZone != cr.Spec.AvailabilityZone {
					t.Errorf("AcceptedSpec.AvailabilityZone: want %q, got %q",
						cr.Spec.AvailabilityZone, updated.Status.AcceptedSpec.AvailabilityZone)
				}
			}
		})
	}
}

func TestCommittedResourceController_InactiveStates(t *testing.T) {
	tests := []struct {
		name  string
		state v1alpha1.CommitmentStatus
	}{
		{name: "superseded: child Reservations deleted, Ready=False", state: v1alpha1.CommitmentStatusSuperseded},
		{name: "expired: child Reservations deleted, Ready=False", state: v1alpha1.CommitmentStatusExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			cr := newTestCommittedResource("test-cr", tt.state)
			existing := &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cr-0",
					Labels: map[string]string{
						v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
					},
				},
				Spec: v1alpha1.ReservationSpec{
					Type: v1alpha1.ReservationTypeCommittedResource,
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						CommitmentUUID: "test-uuid-1234",
					},
				},
			}
			k8sClient := newCRTestClient(scheme, cr, existing)
			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, string(tt.state))
			if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
				t.Errorf("expected 0 child reservations after %s, got %d", tt.state, got)
			}
		})
	}
}

// ============================================================================
// Tests: placement failure paths
// ============================================================================

func TestCommittedResourceController_PlacementFailure(t *testing.T) {
	// Knowledge absent → placement fails. Tests diverging behavior by state and AllowRejection.
	tests := []struct {
		name           string
		state          v1alpha1.CommitmentStatus
		allowRejection bool
		expectedReason string
		expectRequeue  bool
	}{
		{
			name:           "pending AllowRejection=true: rejects on failure, no retry",
			state:          v1alpha1.CommitmentStatusPending,
			allowRejection: true,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "pending AllowRejection=false: retries on failure",
			state:          v1alpha1.CommitmentStatusPending,
			allowRejection: false,
			expectedReason: "Reserving",
			expectRequeue:  true,
		},
		{
			name:           "confirmed AllowRejection=true: rejects on failure, no retry",
			state:          v1alpha1.CommitmentStatusConfirmed,
			allowRejection: true,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "confirmed AllowRejection=false: retries on failure",
			state:          v1alpha1.CommitmentStatusConfirmed,
			allowRejection: false,
			expectedReason: "Reserving",
			expectRequeue:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			cr := newTestCommittedResource("test-cr", tt.state)
			cr.Spec.AllowRejection = tt.allowRejection
			k8sClient := newCRTestClient(scheme, cr) // no Knowledge → placement fails
			controller := &CommittedResourceController{
				Client: k8sClient,
				Scheme: scheme,
				Conf:   CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: 1 * time.Minute}},
			}

			result, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name))
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, tt.expectedReason)
			if tt.expectRequeue && result.RequeueAfter == 0 {
				t.Errorf("expected requeue after failure, got none")
			}
			if !tt.expectRequeue && result.RequeueAfter != 0 {
				t.Errorf("expected no requeue after rejection, got RequeueAfter=%v", result.RequeueAfter)
			}
			if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
				t.Errorf("expected 0 child reservations after failure, got %d", got)
			}
		})
	}
}

func TestCommittedResourceController_Rollback(t *testing.T) {
	scheme := newCRTestScheme(t)

	// CR at generation 2; AcceptedSpec reflects what was accepted at generation 1.
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	cr.Generation = 2
	acceptedSpec := cr.Spec
	cr.Status.AcceptedSpec = &acceptedSpec

	// Existing reservation with stale ParentGeneration from the previous generation.
	existing := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cr-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			AvailabilityZone: "test-az",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("4Gi"),
				hv1.ResourceCPU:    resource.MustParse("2"),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID:   "test-uuid-1234",
				ProjectID:        "test-project",
				DomainID:         "test-domain",
				ResourceGroup:    "test-group",
				ParentGeneration: 1, // stale
			},
		},
	}

	k8sClient := newCRTestClient(scheme, cr, existing, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

	if err := controller.rollbackToAccepted(context.Background(), logr.Discard(), cr); err != nil {
		t.Fatalf("rollbackToAccepted: %v", err)
	}

	var res v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "test-cr-0"}, &res); err != nil {
		t.Fatalf("get reservation: %v", err)
	}
	if got := res.Spec.CommittedResourceReservation.ParentGeneration; got != cr.Generation {
		t.Errorf("ParentGeneration: want %d, got %d", cr.Generation, got)
	}
}

// TestCommittedResourceController_RollbackUsesAcceptedSpecAZ verifies that rollbackToAccepted
// targets the AZ from AcceptedSpec, not from the current (mutated) Spec. This is the core fix
// for the oscillation bug where a failed AZ change left the CR stuck placing reservations in
// the wrong AZ on every retry.
func TestCommittedResourceController_RollbackUsesAcceptedSpecAZ(t *testing.T) {
	scheme := newCRTestScheme(t)

	// Spec has been mutated to a new AZ that failed placement.
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	cr.Spec.AvailabilityZone = "new-az" // the failed AZ
	cr.Generation = 2

	acceptedSpec := cr.Spec
	acceptedSpec.AvailabilityZone = "accepted-az" // last successfully placed AZ
	cr.Status.AcceptedSpec = &acceptedSpec

	// Existing reservation was placed in the wrong AZ by the failed rollback.
	existing := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cr-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			AvailabilityZone: "new-az", // wrong AZ
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("4Gi"),
				hv1.ResourceCPU:    resource.MustParse("2"),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID:   "test-uuid-1234",
				ProjectID:        "test-project",
				DomainID:         "test-domain",
				ResourceGroup:    "test-group",
				ParentGeneration: 2,
			},
		},
	}

	k8sClient := newCRTestClient(scheme, cr, existing, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

	if err := controller.rollbackToAccepted(context.Background(), logr.Discard(), cr); err != nil {
		t.Fatalf("rollbackToAccepted: %v", err)
	}

	// The reservation manager deletes the wrong-AZ reservation and creates a new one
	// with the accepted AZ from AcceptedSpec.
	var list v1alpha1.ReservationList
	if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 reservation after rollback, got %d", len(list.Items))
	}
	if got := list.Items[0].Spec.AvailabilityZone; got != "accepted-az" {
		t.Errorf("rollback: reservation AZ: want %q (from AcceptedSpec), got %q (wrong: from current Spec)", "accepted-az", got)
	}
}

// TestCommittedResourceController_RollbackNilAcceptedSpec verifies that when AcceptedSpec is
// absent (pre-dates the field), rollbackToAccepted deletes child reservations rather than
// attempting a rollback with stale/wrong placement data. The controller repairs state on
// the next reconcile via ApplyCommitmentState.
func TestCommittedResourceController_RollbackNilAcceptedSpec(t *testing.T) {
	scheme := newCRTestScheme(t)

	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	cr.Generation = 2
	// AcceptedSpec intentionally nil — simulates a CR that was never successfully accepted.

	existing := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cr-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: "test-uuid-1234",
				ProjectID:      "test-project",
			},
		},
	}

	k8sClient := newCRTestClient(scheme, cr, existing, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

	if err := controller.rollbackToAccepted(context.Background(), logr.Discard(), cr); err != nil {
		t.Fatalf("rollbackToAccepted: %v", err)
	}

	var list v1alpha1.ReservationList
	if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected all reservations deleted when AcceptedSpec is nil, got %d", len(list.Items))
	}
}

// increments on each placement failure (AllowRejection=false) and resets to 0 on acceptance.
// It also checks that the retry delay grows with each failure.
// TestCommittedResourceController_RejectedStaysRejected verifies that a CR rejected on one
// reconcile cycle stays rejected on subsequent cycles triggered by Reservation watch events,
// without re-applying the bad spec. This is the oscillation regression test: without the
// isRejectedForGeneration guard the controller would re-apply the bad spec on every
// Reservation watch re-enqueue, undoing the rollback each time.
func TestCommittedResourceController_RejectedStaysRejected(t *testing.T) {
	tests := []struct {
		name  string
		state v1alpha1.CommitmentStatus
	}{
		{name: "confirmed", state: v1alpha1.CommitmentStatusConfirmed},
		{name: "pending", state: v1alpha1.CommitmentStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)

			// CR was previously accepted at AZ "accepted-az".
			cr := newTestCommittedResource("test-cr", tt.state)
			cr.Spec.AllowRejection = true
			cr.Spec.AvailabilityZone = "bad-az" // spec was mutated to a failing AZ
			cr.Generation = 2
			acceptedSpec := cr.Spec.DeepCopy()
			acceptedSpec.AvailabilityZone = "accepted-az"
			cr.Status.AcceptedSpec = acceptedSpec

			// No Knowledge → placement always fails.
			k8sClient := newCRTestClient(scheme, cr)
			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

			// Reconcile 1: applies bad spec → fails → rollback + Rejected.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("reconcile 1: %v", err)
			}
			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)

			// Reconcile 2: simulates Reservation watch re-enqueue after rollback.
			// Must stay Rejected without re-applying the bad spec.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("reconcile 2: %v", err)
			}
			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)

			// Reconcile 3: another watch event — still stable.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("reconcile 3: %v", err)
			}
			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)

			// For committed state: rollback reservations should be in accepted-az, not bad-az.
			if tt.state == v1alpha1.CommitmentStatusConfirmed {
				var list v1alpha1.ReservationList
				if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					t.Fatalf("list reservations: %v", err)
				}
				for _, res := range list.Items {
					if res.Spec.AvailabilityZone == "bad-az" {
						t.Errorf("rollback reservation still points to bad-az after %d reconciles — oscillation not fixed", 3)
					}
				}
			}
		})
	}
}

func TestCommittedResourceController_RetryBackoff(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	cr.Spec.AllowRejection = false
	base := 30 * time.Second
	k8sClient := newCRTestClient(scheme, cr) // no Knowledge → placement fails
	controller := &CommittedResourceController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: base}},
	}

	getCR := func() v1alpha1.CommittedResource {
		t.Helper()
		var updated v1alpha1.CommittedResource
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &updated); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		return updated
	}

	// First failure: Reserving condition does not exist yet → delay = base * 2^0.
	result1, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name))
	if err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if result1.RequeueAfter != base {
		t.Errorf("after failure 1: RequeueAfter want %v, got %v", base, result1.RequeueAfter)
	}
	cond1 := meta.FindStatusCondition(getCR().Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond1 == nil || cond1.Reason != v1alpha1.CommittedResourceReasonReserving {
		t.Fatalf("after failure 1: expected Ready=False/Reserving condition")
	}

	// Second failure: simulate base seconds elapsed by back-dating the condition's LastTransitionTime.
	cr2 := getCR()
	old2 := cr2.DeepCopy()
	for i, c := range cr2.Status.Conditions {
		if c.Type == v1alpha1.CommittedResourceConditionReady {
			cr2.Status.Conditions[i].LastTransitionTime = metav1.NewTime(time.Now().Add(-base))
		}
	}
	if err := k8sClient.Status().Patch(context.Background(), &cr2, client.MergeFrom(old2)); err != nil {
		t.Fatalf("back-date condition: %v", err)
	}
	result2, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name))
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if result2.RequeueAfter != 2*base {
		t.Errorf("after failure 2: RequeueAfter want %v, got %v", 2*base, result2.RequeueAfter)
	}

	// Add Knowledge so placement succeeds; simulate reservation controller marking ready.
	if err := k8sClient.Create(context.Background(), newTestFlavorKnowledge()); err != nil {
		t.Fatalf("create knowledge: %v", err)
	}
	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile 3 (apply): %v", err)
	}
	setChildReservationsReady(t, k8sClient, cr.Spec.CommitmentUUID)
	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile 4 (accept): %v", err)
	}

	// After acceptance the Reserving condition is gone (replaced by Ready=True/Accepted).
	acceptedCond := meta.FindStatusCondition(getCR().Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if acceptedCond == nil || acceptedCond.Status != metav1.ConditionTrue {
		t.Errorf("after acceptance: expected Ready=True condition")
	}
}

// ============================================================================
// Tests: reconcileCoresHeadroom
// ============================================================================

// newTestCoresCR creates a CommittedResource with ResourceType=cores.
func newTestCoresCR(name string, state v1alpha1.CommitmentStatus, cores int64, allowRejection bool) *v1alpha1.CommittedResource {
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "cores-uuid-1234",
			FlavorGroupName:  "test-group",
			ResourceType:     v1alpha1.CommittedResourceTypeCores,
			Amount:           *resource.NewQuantity(cores, resource.DecimalSI),
			AvailabilityZone: "test-az",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
			State:            state,
			AllowRejection:   allowRejection,
		},
	}
}

// newTestFlavorGroupCapacity creates a FlavorGroupCapacity CRD with the given total cores.
func newTestFlavorGroupCapacity(flavorGroup, az string, totalCores int64) *v1alpha1.FlavorGroupCapacity {
	return &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: metav1.ObjectMeta{Name: flavorGroup + "-" + az},
		Spec: v1alpha1.FlavorGroupCapacitySpec{
			FlavorGroup:      flavorGroup,
			AvailabilityZone: az,
		},
		Status: v1alpha1.FlavorGroupCapacityStatus{
			TotalCapacity: map[string]resource.Quantity{
				string(v1alpha1.CommittedResourceTypeCores): *resource.NewQuantity(totalCores, resource.DecimalSI),
			},
		},
	}
}

func TestCommittedResourceController_CoresHeadroom(t *testing.T) {
	tests := []struct {
		name           string
		state          v1alpha1.CommitmentStatus
		requestedCores int64
		totalCores     int64
		allowRejection bool
		// other accepted CPU CRs consuming cores
		existingCores int64
		// capacity CRD missing entirely
		noCapacityCRD bool
		// capacity CRD present but TotalCapacity["cores"] not set
		noCoreCapacity bool
		expectedStatus metav1.ConditionStatus
		expectedReason string
		expectRequeue  bool
	}{
		{
			name:           "accepted: sufficient headroom",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 4,
			totalCores:     16,
			existingCores:  0,
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "Accepted",
		},
		{
			name:           "accepted: headroom exactly meets request",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 8,
			totalCores:     16,
			existingCores:  8,
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "Accepted",
		},
		{
			name:           "rejected: insufficient headroom, AllowRejection=true",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 10,
			totalCores:     16,
			existingCores:  8,
			allowRejection: true,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "retry: insufficient headroom, AllowRejection=false",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 10,
			totalCores:     16,
			existingCores:  8,
			allowRejection: false,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Reserving",
			expectRequeue:  true,
		},
		{
			name:           "retry: FlavorGroupCapacity CRD not found",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 4,
			noCapacityCRD:  true,
			allowRejection: true,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Reserving",
			expectRequeue:  true,
		},
		{
			name:           "retry: TotalCapacity[cores] not set",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 4,
			noCoreCapacity: true,
			allowRejection: true,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Reserving",
			expectRequeue:  true,
		},
		{
			// TotalCapacity["cores"]=0 means the capacity controller probed and found no
			// eligible hosts (e.g. HANA flavor groups in a QA cluster). This must reject
			// immediately rather than retrying, to avoid API timeouts.
			name:           "rejected immediately: zero CPU capacity, AllowRejection=true",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 4,
			totalCores:     0,
			allowRejection: true,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "stays rejected: already rejected for current generation",
			state:          v1alpha1.CommitmentStatusConfirmed,
			requestedCores: 4,
			totalCores:     16,
			allowRejection: true,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			cr := newTestCoresCR("test-cr", tt.state, tt.requestedCores, tt.allowRejection)

			objects := []client.Object{cr}

			if !tt.noCapacityCRD {
				if tt.noCoreCapacity {
					// Capacity CRD present but without the cores key.
					fgc := &v1alpha1.FlavorGroupCapacity{
						ObjectMeta: metav1.ObjectMeta{Name: "test-group-test-az"},
						Spec: v1alpha1.FlavorGroupCapacitySpec{
							FlavorGroup:      "test-group",
							AvailabilityZone: "test-az",
						},
						Status: v1alpha1.FlavorGroupCapacityStatus{
							TotalCapacity: map[string]resource.Quantity{},
						},
					}
					objects = append(objects, fgc)
				} else {
					objects = append(objects, newTestFlavorGroupCapacity("test-group", "test-az", tt.totalCores))
				}
			}

			if tt.existingCores > 0 {
				// An already-accepted cores CR consuming existingCores.
				otherCR := newTestCoresCR("other-cr", v1alpha1.CommitmentStatusConfirmed, tt.existingCores, false)
				otherCR.Spec.CommitmentUUID = "other-uuid-5678"
				otherSpec := otherCR.Spec
				otherCR.Status.AcceptedSpec = &otherSpec
				objects = append(objects, otherCR)
			}

			k8sClient := newCRTestClient(scheme, objects...)

			// For the "stays rejected" test, pre-set Rejected condition at current generation.
			if tt.name == "stays rejected: already rejected for current generation" {
				var fetched v1alpha1.CommittedResource
				if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &fetched); err != nil {
					t.Fatalf("get CR: %v", err)
				}
				fetched.Status.Conditions = []metav1.Condition{{
					Type:               v1alpha1.CommittedResourceConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             v1alpha1.CommittedResourceReasonRejected,
					ObservedGeneration: fetched.Generation,
					LastTransitionTime: metav1.Now(),
				}}
				if err := k8sClient.Status().Update(context.Background(), &fetched); err != nil {
					t.Fatalf("set rejected status: %v", err)
				}
			}

			controller := &CommittedResourceController{
				Client: k8sClient,
				Scheme: scheme,
				Conf:   CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: 1 * time.Minute}},
			}

			result, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name))
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			assertCondition(t, k8sClient, cr.Name, tt.expectedStatus, tt.expectedReason)

			if tt.expectRequeue && result.RequeueAfter == 0 {
				t.Errorf("expected requeue, got none")
			}
			if !tt.expectRequeue && result.RequeueAfter != 0 {
				t.Errorf("expected no requeue, got RequeueAfter=%v", result.RequeueAfter)
			}

			// CPU CRs must never produce Reservation CRDs.
			if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
				t.Errorf("expected 0 child reservations for cores CR, got %d", got)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	base := 30 * time.Second
	maxDelay := 30 * time.Minute
	controller := &CommittedResourceController{
		Conf: CommittedResourceControllerConfig{
			RequeueIntervalRetry: metav1.Duration{Duration: base},
			MaxRequeueInterval:   metav1.Duration{Duration: maxDelay},
		},
	}
	tests := []struct {
		elapsed time.Duration
		want    time.Duration
	}{
		// Windows with base=30s: [0,30s)→30s, [30s,60s)→60s, [60s,120s)→120s,
		// [120s,240s)→240s, [240s,480s)→480s, [480s,960s)→960s(16m), [960s,∞)→capped 30m.
		// Use mid-window values to avoid boundary flakiness from time.Since epsilon.
		{0, 30 * time.Second},                // start of first window
		{15 * time.Second, base},             // mid [0s,30s)
		{45 * time.Second, 2 * base},         // mid [30s,60s)
		{90 * time.Second, 4 * base},         // mid [60s,120s)
		{3 * time.Minute, 8 * base},          // mid [120s,240s)
		{6 * time.Minute, 16 * base},         // mid [240s,480s)
		{12 * time.Minute, 32 * base},        // mid [480s,960s) = 16m
		{20 * time.Minute, 30 * time.Minute}, // [960s,∞) → capped
		{60 * time.Minute, 30 * time.Minute}, // well beyond cap
	}
	for _, tt := range tests {
		ltt := metav1.NewTime(time.Now().Add(-tt.elapsed))
		cr := &v1alpha1.CommittedResource{
			Status: v1alpha1.CommittedResourceStatus{
				Conditions: []metav1.Condition{
					{
						Type:               v1alpha1.CommittedResourceConditionReady,
						Status:             metav1.ConditionFalse,
						Reason:             v1alpha1.CommittedResourceReasonReserving,
						LastTransitionTime: ltt,
					},
				},
			},
		}
		if got := controller.retryDelay(cr); got != tt.want {
			t.Errorf("elapsed=%v: want %v, got %v", tt.elapsed, tt.want, got)
		}
	}
}

func TestCommittedResourceController_BadSpec(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cr",
		},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "x", // too short, fails commitmentUUIDPattern
			FlavorGroupName:  "test-group",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           resource.MustParse("4Gi"),
			AvailabilityZone: "test-az",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
			State:            v1alpha1.CommitmentStatusConfirmed,
		},
	}
	k8sClient := newCRTestClient(scheme, cr, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: CommittedResourceControllerConfig{}}

	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, "Rejected")
	if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
		t.Errorf("expected 0 child reservations after bad-spec rejection, got %d", got)
	}
}

// ============================================================================
// Tests: checkChildReservationStatus generation guard
// ============================================================================

// TestCheckChildReservationStatus_GenerationGuard verifies the two-pass logic that
// distinguishes a stale Ready=False (previous generation) from a current failure.
func TestCheckChildReservationStatus_GenerationGuard(t *testing.T) {
	tests := []struct {
		name          string
		obsGen        int64
		condStatus    metav1.ConditionStatus // "" = no condition set
		condMessage   string
		wantAllReady  bool
		wantAnyFailed bool
		wantReason    string
	}{
		{
			name:          "Ready=False at stale generation: treated as pending",
			obsGen:        1,
			condStatus:    metav1.ConditionFalse,
			condMessage:   "no hosts available",
			wantAllReady:  false,
			wantAnyFailed: false,
		},
		{
			name:          "Ready=False at current generation: is a current failure",
			obsGen:        2,
			condStatus:    metav1.ConditionFalse,
			condMessage:   "no hosts available",
			wantAllReady:  false,
			wantAnyFailed: true,
			wantReason:    "no hosts available",
		},
		{
			name:         "Ready=True at current generation: allReady",
			obsGen:       2,
			condStatus:   metav1.ConditionTrue,
			wantAllReady: true,
		},
		{
			name:          "no condition yet at current generation: still pending",
			obsGen:        2,
			condStatus:    "", // no condition
			wantAllReady:  false,
			wantAnyFailed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
			cr.Generation = 2

			child := &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cr-0",
					Labels: map[string]string{
						v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
					},
				},
				Spec: v1alpha1.ReservationSpec{
					Type: v1alpha1.ReservationTypeCommittedResource,
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						CommitmentUUID:   cr.Spec.CommitmentUUID,
						ParentGeneration: cr.Generation,
					},
				},
			}
			k8sClient := newCRTestClient(scheme, child)

			child.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{
				ObservedParentGeneration: tt.obsGen,
			}
			if tt.condStatus != "" {
				child.Status.Conditions = []metav1.Condition{{
					Type:               v1alpha1.ReservationConditionReady,
					Status:             tt.condStatus,
					Reason:             "Test",
					Message:            tt.condMessage,
					LastTransitionTime: metav1.Now(),
				}}
			}
			if err := k8sClient.Status().Update(context.Background(), child); err != nil {
				t.Fatalf("set reservation status: %v", err)
			}

			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme}
			allReady, anyFailed, reason, err := controller.checkChildReservationStatus(context.Background(), cr, 1)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allReady != tt.wantAllReady {
				t.Errorf("allReady: want %v, got %v", tt.wantAllReady, allReady)
			}
			if anyFailed != tt.wantAnyFailed {
				t.Errorf("anyFailed: want %v, got %v", tt.wantAnyFailed, anyFailed)
			}
			if reason != tt.wantReason {
				t.Errorf("reason: want %q, got %q", tt.wantReason, reason)
			}
		})
	}
}

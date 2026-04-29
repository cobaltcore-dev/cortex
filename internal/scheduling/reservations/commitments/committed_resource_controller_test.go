// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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
// The finalizer is pre-populated so tests can call Reconcile once without a
// separate finalizer-add round-trip.
func newTestCommittedResource(name string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{crFinalizer},
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
// Reservations for the given commitmentUUID as Ready=True.
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
			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

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
				if updated.Status.AcceptedAmount == nil {
					t.Errorf("expected AcceptedAmount to be set on acceptance")
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
			controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

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
			name:           "pending: always rejects on failure, no retry",
			state:          v1alpha1.CommitmentStatusPending,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "guaranteed AllowRejection=true: rejects on failure, no retry",
			state:          v1alpha1.CommitmentStatusGuaranteed,
			allowRejection: true,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "confirmed AllowRejection=true: rejects on failure, no retry",
			state:          v1alpha1.CommitmentStatusConfirmed,
			allowRejection: true,
			expectedReason: "Rejected",
			expectRequeue:  false,
		},
		{
			name:           "guaranteed AllowRejection=false: retries on failure",
			state:          v1alpha1.CommitmentStatusGuaranteed,
			allowRejection: false,
			expectedReason: "Reserving",
			expectRequeue:  true,
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
				Conf:   Config{RequeueIntervalRetry: 1 * time.Minute},
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

func TestCommittedResourceController_BadSpec(t *testing.T) {
	// Invalid UUID fails commitmentUUIDPattern — permanently broken regardless of AllowRejection.
	scheme := newCRTestScheme(t)
	cr := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cr",
			Finalizers: []string{crFinalizer},
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
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, "Rejected")
	if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
		t.Errorf("expected 0 child reservations after bad-spec rejection, got %d", got)
	}
}

func TestCommittedResourceController_Idempotent(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	k8sClient := newCRTestClient(scheme, cr, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

	// Round 1: creates reservation, waits for placement.
	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	// Simulate reservation controller setting Ready=True.
	setChildReservationsReady(t, k8sClient, cr.Spec.CommitmentUUID)
	// Rounds 2 and 3: accepts, then stays accepted.
	for i := 2; i <= 3; i++ {
		if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}

	if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 1 {
		t.Errorf("expected 1 child reservation after 3 reconciles (idempotency), got %d", got)
	}
	assertCondition(t, k8sClient, cr.Name, metav1.ConditionTrue, "Accepted")
}

func TestCommittedResourceController_Deletion(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
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
				CommitmentUUID: "test-uuid-1234",
			},
		},
	}
	k8sClient := newCRTestClient(scheme, cr, child)
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

	if err := k8sClient.Delete(context.Background(), cr); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := countChildReservations(t, k8sClient, cr.Spec.CommitmentUUID); got != 0 {
		t.Errorf("expected 0 child reservations after deletion, got %d", got)
	}
	var deleted v1alpha1.CommittedResource
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &deleted); err == nil {
		t.Errorf("expected CR to be gone after deletion, but it still exists with finalizers=%v", deleted.Finalizers)
	}
}

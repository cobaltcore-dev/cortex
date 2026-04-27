// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"strings"
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

// newTestCommittedResource returns a CommittedResource with sensible defaults for testing.
func newTestCommittedResource(name string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
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

// newTestFlavorKnowledge returns a Knowledge CRD with a single flavor group for testing.
// The flavor group has one flavor of 4 GiB so a 4 GiB commitment produces exactly one slot.
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

// assertCondition checks that the CR has the expected Ready condition status and reason.
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

// countChildReservations returns the number of Reservation CRDs whose name starts with the CR name prefix.
func countChildReservations(t *testing.T, k8sClient client.Client, crName string) int {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("failed to list reservations: %v", err)
	}
	prefix := crName + "-"
	count := 0
	for _, r := range list.Items {
		if strings.HasPrefix(r.Name, prefix) {
			count++
		}
	}
	return count
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
		expectedSlots  int // expected child Reservation count after reconcile
		// Knowledge CRD is only needed for active states (guaranteed/confirmed).
		// planned/pending short-circuit before ApplyCommitmentState, so it is omitted.
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
			name:           "pending: no Reservations created, Ready=False/Planned",
			state:          v1alpha1.CommitmentStatusPending,
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "Planned",
			expectedSlots:  0,
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

			// First reconcile adds finalizer and returns early.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("first reconcile error: %v", err)
			}
			// Second reconcile runs the actual state logic.
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("second reconcile error: %v", err)
			}

			assertCondition(t, k8sClient, cr.Name, tt.expectedStatus, tt.expectedReason)
			if got := countChildReservations(t, k8sClient, cr.Name); got != tt.expectedSlots {
				t.Errorf("expected %d child reservations, got %d", tt.expectedSlots, got)
			}

			// For active states, verify AcceptedAmount is set and child follows naming convention.
			if tt.expectedSlots > 0 {
				var updated v1alpha1.CommittedResource
				if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &updated); err != nil {
					t.Fatalf("get CR: %v", err)
				}
				if updated.Status.AcceptedAmount == nil {
					t.Errorf("expected AcceptedAmount to be set on acceptance")
				}

				// Child reservation must follow <cr-name>-<slot-index> naming.
				var list v1alpha1.ReservationList
				if err := k8sClient.List(context.Background(), &list, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					t.Fatalf("list reservations: %v", err)
				}
				expectedName := cr.Name + "-0"
				found := false
				for _, r := range list.Items {
					if r.Name == expectedName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected child reservation named %q, not found in %v",
						expectedName, func() []string {
							names := make([]string, len(list.Items))
							for i, r := range list.Items {
								names[i] = r.Name
							}
							return names
						}())
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
			// Pre-existing child reservation that should be cleaned up.
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
				t.Fatalf("first reconcile (finalizer): %v", err)
			}
			if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
				t.Fatalf("second reconcile: %v", err)
			}

			assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, string(tt.state))
			if got := countChildReservations(t, k8sClient, cr.Name); got != 0 {
				t.Errorf("expected 0 child reservations after %s, got %d", tt.state, got)
			}
		})
	}
}

func TestCommittedResourceController_MissingKnowledge(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	// No Knowledge CRD — controller should requeue, not error or set Ready=True.
	k8sClient := newCRTestClient(scheme, cr)
	controller := &CommittedResourceController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   Config{RequeueIntervalRetry: 5 * time.Minute},
	}

	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	result, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name))
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Errorf("expected requeue when knowledge not ready, got none")
	}
	if got := countChildReservations(t, k8sClient, cr.Name); got != 0 {
		t.Errorf("expected no reservations created when knowledge missing, got %d", got)
	}
}

func TestCommittedResourceController_UnsupportedResourceType(t *testing.T) {
	scheme := newCRTestScheme(t)
	// Invalid resource type causes FromCommittedResource to fail → rollback path.
	cr := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cr"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "test-uuid-1234",
			FlavorGroupName:  "test-group",
			ResourceType:     v1alpha1.CommittedResourceTypeCores, // unsupported → triggers rejection
			Amount:           resource.MustParse("4"),
			AvailabilityZone: "test-az",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
			State:            v1alpha1.CommitmentStatusConfirmed,
		},
	}
	k8sClient := newCRTestClient(scheme, cr, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	assertCondition(t, k8sClient, cr.Name, metav1.ConditionFalse, "Rejected")
	if got := countChildReservations(t, k8sClient, cr.Name); got != 0 {
		t.Errorf("expected 0 child reservations after rollback, got %d", got)
	}
}

func TestCommittedResourceController_Idempotent(t *testing.T) {
	scheme := newCRTestScheme(t)
	cr := newTestCommittedResource("test-cr", v1alpha1.CommitmentStatusConfirmed)
	k8sClient := newCRTestClient(scheme, cr, newTestFlavorKnowledge())
	controller := &CommittedResourceController{Client: k8sClient, Scheme: scheme, Conf: Config{}}

	// Reconcile three times — slot count must stay at 1.
	for i := range 3 {
		if _, err := controller.Reconcile(context.Background(), reconcileReq(cr.Name)); err != nil {
			t.Fatalf("reconcile %d: %v", i+1, err)
		}
	}

	if got := countChildReservations(t, k8sClient, cr.Name); got != 1 {
		t.Errorf("expected 1 child reservation after 3 reconciles (idempotency), got %d", got)
	}
	assertCondition(t, k8sClient, cr.Name, metav1.ConditionTrue, "Accepted")
}

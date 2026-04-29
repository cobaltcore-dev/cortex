// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

// Integration tests for the CR lifecycle spanning CommittedResourceController and
// CommitmentReservationController. These tests drive both controllers against a shared
// fake client and verify the end-to-end state transitions without mocking internal logic.
//
// Scope:
//   - State transition: planned → confirmed produces child Reservations
//   - State transition: confirmed → expired cleans up child Reservations
//   - Reservation controller places a child Reservation created by the CR controller
//   - CR deletion removes all child Reservations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// crIntegrationEnv holds shared state for integration tests.
type crIntegrationEnv struct {
	k8sClient       client.Client
	crController    *CommittedResourceController
	resController   *CommitmentReservationController
	schedulerServer *httptest.Server
}

func newCRIntegrationEnv(t *testing.T) *crIntegrationEnv {
	t.Helper()
	scheme := newCRTestScheme(t)

	hypervisor := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newTestFlavorKnowledge(), hypervisor).
		WithStatusSubresource(
			&v1alpha1.CommittedResource{},
			&v1alpha1.Reservation{},
			&v1alpha1.Knowledge{},
		).
		Build()

	schedulerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := &schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{"host-1"}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("scheduler encode: %v", err)
		}
	}))

	crCtrl := &CommittedResourceController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   Config{RequeueIntervalRetry: 5 * time.Minute},
	}

	resCtrl := &CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf: Config{
			SchedulerURL:          schedulerServer.URL,
			AllocationGracePeriod: 15 * time.Minute,
			RequeueIntervalActive: 5 * time.Minute,
		},
	}
	if err := resCtrl.Init(context.Background(), k8sClient, resCtrl.Conf); err != nil {
		t.Fatalf("resCtrl.Init: %v", err)
	}

	return &crIntegrationEnv{
		k8sClient:       k8sClient,
		crController:    crCtrl,
		resController:   resCtrl,
		schedulerServer: schedulerServer,
	}
}

func (e *crIntegrationEnv) close() { e.schedulerServer.Close() }

func (e *crIntegrationEnv) reconcileCR(t *testing.T, crName string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: crName}}
	if _, err := e.crController.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("CR reconcile: %v", err)
	}
}

func (e *crIntegrationEnv) reconcileReservation(t *testing.T, resName string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: resName}}
	if _, err := e.resController.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reservation reconcile %s: %v", resName, err)
	}
}

func (e *crIntegrationEnv) listChildReservations(t *testing.T, crName string) []v1alpha1.Reservation {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := e.k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	prefix := crName + "-"
	var children []v1alpha1.Reservation
	for _, r := range list.Items {
		if strings.HasPrefix(r.Name, prefix) {
			children = append(children, r)
		}
	}
	return children
}

func (e *crIntegrationEnv) getCR(t *testing.T, name string) v1alpha1.CommittedResource {
	t.Helper()
	var cr v1alpha1.CommittedResource
	if err := e.k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &cr); err != nil {
		t.Fatalf("get CR %s: %v", name, err)
	}
	return cr
}

// reconcileChildReservations runs the reservation controller twice on every child Reservation
// for crName (first reconcile sets TargetHost, second sets Ready=True), then re-reconciles
// the CR so it can observe the placement outcomes.
func (e *crIntegrationEnv) reconcileChildReservations(t *testing.T, crName string) {
	t.Helper()
	for _, res := range e.listChildReservations(t, crName) {
		e.reconcileReservation(t, res.Name) // calls scheduler → sets TargetHost
		e.reconcileReservation(t, res.Name) // syncs TargetHost to Status → Ready=True
	}
	e.reconcileCR(t, crName)
}

// ============================================================================
// Integration tests
// ============================================================================

// TestCRLifecycle_PlannedToConfirmed verifies that transitioning a CR from planned
// to confirmed causes the CR controller to create child Reservation CRDs.
func TestCRLifecycle_PlannedToConfirmed(t *testing.T) {
	env := newCRIntegrationEnv(t)
	defer env.close()

	cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusPlanned)
	if err := env.k8sClient.Create(context.Background(), cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Reconcile as planned: finalizer added, no Reservations.
	env.reconcileCR(t, cr.Name)
	env.reconcileCR(t, cr.Name)
	if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
		t.Fatalf("planned: expected 0 reservations, got %d", len(got))
	}
	crState := env.getCR(t, cr.Name)
	cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil || cond.Reason != "Planned" {
		t.Errorf("planned: expected Reason=Planned, got %v", cond)
	}

	// Transition to confirmed.
	patch := client.MergeFrom(crState.DeepCopy())
	crState.Spec.State = v1alpha1.CommitmentStatusConfirmed
	if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
		t.Fatalf("patch state to confirmed: %v", err)
	}

	env.reconcileCR(t, cr.Name)

	children := env.listChildReservations(t, cr.Name)
	if len(children) != 1 {
		t.Fatalf("confirmed: expected 1 reservation, got %d", len(children))
	}
	// Run reservation controller to place the slot, then re-reconcile the CR to accept.
	env.reconcileChildReservations(t, cr.Name)

	crState = env.getCR(t, cr.Name)
	if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
		t.Errorf("confirmed: expected Ready=True")
	}
}

// TestCRLifecycle_ConfirmedToExpired verifies that transitioning a CR to expired
// deletes all child Reservation CRDs and marks Ready=False.
func TestCRLifecycle_ConfirmedToExpired(t *testing.T) {
	env := newCRIntegrationEnv(t)
	defer env.close()

	cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
	if err := env.k8sClient.Create(context.Background(), cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Bring to confirmed+Ready=True: finalizer → create Reservations → place → accept.
	env.reconcileCR(t, cr.Name)                // adds finalizer
	env.reconcileCR(t, cr.Name)                // creates Reservations, waits for placement
	env.reconcileChildReservations(t, cr.Name) // places slots + re-reconciles CR → Ready=True

	if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
		t.Fatalf("pre-expire: expected 1 reservation, got %d", len(got))
	}

	// Transition to expired.
	crState := env.getCR(t, cr.Name)
	patch := client.MergeFrom(crState.DeepCopy())
	crState.Spec.State = v1alpha1.CommitmentStatusExpired
	if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
		t.Fatalf("patch state to expired: %v", err)
	}

	env.reconcileCR(t, cr.Name)

	if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
		t.Errorf("expired: expected 0 reservations, got %d", len(got))
	}
	crState = env.getCR(t, cr.Name)
	cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("expired: expected Ready=False, got %v", cond)
	}
	if cond != nil && cond.Reason != string(v1alpha1.CommitmentStatusExpired) {
		t.Errorf("expired: expected Reason=%s, got %s", v1alpha1.CommitmentStatusExpired, cond.Reason)
	}
}

// TestCRLifecycle_ReservationControllerPlacesChild verifies that after the CR controller
// creates a child Reservation, the ReservationController can place it (scheduler call →
// TargetHost set → Ready=True on the Reservation).
func TestCRLifecycle_ReservationControllerPlacesChild(t *testing.T) {
	env := newCRIntegrationEnv(t)
	defer env.close()

	cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
	if err := env.k8sClient.Create(context.Background(), cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// CR controller creates child Reservation.
	env.reconcileCR(t, cr.Name)
	env.reconcileCR(t, cr.Name)

	children := env.listChildReservations(t, cr.Name)
	if len(children) != 1 {
		t.Fatalf("expected 1 child reservation, got %d", len(children))
	}
	child := children[0]

	// Reservation controller places it (first reconcile: calls scheduler → sets TargetHost).
	env.reconcileReservation(t, child.Name)

	var afterFirst v1alpha1.Reservation
	if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: child.Name}, &afterFirst); err != nil {
		t.Fatalf("get reservation after first reconcile: %v", err)
	}
	if afterFirst.Spec.TargetHost == "" {
		t.Fatalf("expected TargetHost set after first reservation reconcile")
	}

	// Second reconcile: syncs TargetHost to Status, sets Ready=True.
	env.reconcileReservation(t, child.Name)

	var afterSecond v1alpha1.Reservation
	if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: child.Name}, &afterSecond); err != nil {
		t.Fatalf("get reservation after second reconcile: %v", err)
	}
	if !meta.IsStatusConditionTrue(afterSecond.Status.Conditions, v1alpha1.ReservationConditionReady) {
		t.Errorf("expected reservation Ready=True after placement, got %v", afterSecond.Status.Conditions)
	}
	if afterSecond.Status.Host != "host-1" {
		t.Errorf("expected Status.Host=host-1, got %q", afterSecond.Status.Host)
	}
}

// TestCRLifecycle_Deletion verifies that deleting a CR cleans up all child Reservations.
func TestCRLifecycle_Deletion(t *testing.T) {
	env := newCRIntegrationEnv(t)
	defer env.close()

	cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
	if err := env.k8sClient.Create(context.Background(), cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// newTestCommittedResource pre-populates the finalizer, so Delete() will set
	// DeletionTimestamp without needing a prior reconcile.

	// Pre-create a child Reservation to verify it gets cleaned up on deletion.
	child := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cr-0",
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
	if err := env.k8sClient.Create(context.Background(), child); err != nil {
		t.Fatalf("create child reservation: %v", err)
	}

	// Delete sets DeletionTimestamp (object has finalizer, so it is not removed yet).
	crState := env.getCR(t, cr.Name)
	if err := env.k8sClient.Delete(context.Background(), &crState); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	env.reconcileCR(t, cr.Name)

	if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
		t.Errorf("post-deletion: expected 0 reservations, got %d", len(got))
	}
	// Finalizer removed — object either gone or has no finalizer.
	var final v1alpha1.CommittedResource
	err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &final)
	if client.IgnoreNotFound(err) != nil {
		t.Fatalf("unexpected error after deletion: %v", err)
	}
	if err == nil {
		for _, f := range final.Finalizers {
			if f == crFinalizer {
				t.Errorf("finalizer not removed after deletion reconcile")
			}
		}
	}
}

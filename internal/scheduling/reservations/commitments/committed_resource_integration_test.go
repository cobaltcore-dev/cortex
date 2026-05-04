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
	"k8s.io/apimachinery/pkg/api/resource"
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
		Conf:   CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: 5 * time.Minute}},
	}

	resCtrl := &CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf: ReservationControllerConfig{
			SchedulerURL:          schedulerServer.URL,
			AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute},
			RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
		},
	}
	if err := resCtrl.Init(context.Background(), resCtrl.Conf); err != nil {
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

// TestCRLifecycle covers the multi-step state transitions that require imperative
// mid-test patches and cannot be expressed as a purely declarative table.
func TestCRLifecycle(t *testing.T) {
	t.Run("planned→confirmed: child Reservations created and placed", func(t *testing.T) {
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
		env.reconcileChildReservations(t, cr.Name)

		crState = env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("confirmed: expected Ready=True")
		}
	})

	t.Run("confirmed→expired: child Reservations deleted, CR marked inactive", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Bring to confirmed+Ready=True.
		env.reconcileCR(t, cr.Name)                // adds finalizer
		env.reconcileCR(t, cr.Name)                // creates Reservations
		env.reconcileChildReservations(t, cr.Name) // places slots → Ready=True

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
	})

	t.Run("reservation placement: two reconciles set TargetHost then Ready=True", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)

		children := env.listChildReservations(t, cr.Name)
		if len(children) != 1 {
			t.Fatalf("expected 1 child reservation, got %d", len(children))
		}
		child := children[0]

		// First reconcile: scheduler call → TargetHost written to Spec.
		env.reconcileReservation(t, child.Name)
		var afterFirst v1alpha1.Reservation
		if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: child.Name}, &afterFirst); err != nil {
			t.Fatalf("get reservation after first reconcile: %v", err)
		}
		if afterFirst.Spec.TargetHost == "" {
			t.Fatalf("expected TargetHost set after first reservation reconcile")
		}

		// Second reconcile: TargetHost synced to Status, Ready=True.
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
	})

	t.Run("deletion: finalizer removed, child Reservations cleaned up", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Pre-create a child Reservation to verify it gets cleaned up on deletion.
		// newTestCommittedResource pre-populates the finalizer, so Delete() immediately sets DeletionTimestamp.
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

		crState := env.getCR(t, cr.Name)
		if err := env.k8sClient.Delete(context.Background(), &crState); err != nil {
			t.Fatalf("delete CR: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Errorf("post-deletion: expected 0 reservations, got %d", len(got))
		}
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
	})

	t.Run("confirmed→superseded: child Reservations deleted, CR marked inactive", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Fatalf("pre-supersede: expected 1 reservation, got %d", len(got))
		}

		crState := env.getCR(t, cr.Name)
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.State = v1alpha1.CommitmentStatusSuperseded
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch state to superseded: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Errorf("superseded: expected 0 reservations, got %d", len(got))
		}
		crState = env.getCR(t, cr.Name)
		cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Errorf("superseded: expected Ready=False, got %v", cond)
		}
		if cond != nil && cond.Reason != string(v1alpha1.CommitmentStatusSuperseded) {
			t.Errorf("superseded: expected Reason=%s, got %s", v1alpha1.CommitmentStatusSuperseded, cond.Reason)
		}
	})

	t.Run("idempotency: extra reconciles after Accepted do not create extra slots", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Fatalf("pre-idempotency check: expected 1 reservation, got %d", len(got))
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Errorf("idempotency: expected 1 reservation after extra reconciles, got %d", len(got))
		}
		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("idempotency: expected CR to remain Ready=True after extra reconciles")
		}
	})

	t.Run("AllowRejection=false: stays Reserving when scheduler rejects", func(t *testing.T) {
		hypervisor := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
		env := newIntgEnv(t, []client.Object{newTestFlavorKnowledge(), hypervisor}, intgRejectScheduler)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		// AllowRejection stays false (the default), so placement failure must requeue, not reject.
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
		for range 3 {
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
			var resList v1alpha1.ReservationList
			env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			})
			for _, res := range resList.Items {
				resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			}
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		}

		var final v1alpha1.CommittedResource
		if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, &final); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		cond := meta.FindStatusCondition(final.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			t.Fatalf("no Ready condition")
		}
		if cond.Reason == v1alpha1.CommittedResourceReasonRejected {
			t.Errorf("AllowRejection=false: CR must not transition to Rejected, got Reason=%s", cond.Reason)
		}
		if cond.Reason != v1alpha1.CommittedResourceReasonReserving {
			t.Errorf("AllowRejection=false: expected Reason=Reserving, got %s", cond.Reason)
		}
	})

	t.Run("externally deleted child Reservation is recreated by CR controller", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		children := env.listChildReservations(t, cr.Name)
		if len(children) != 1 {
			t.Fatalf("expected 1 child reservation before deletion, got %d", len(children))
		}

		// Simulate out-of-band deletion of the slot.
		child := children[0]
		if err := env.k8sClient.Delete(context.Background(), &child); err != nil {
			t.Fatalf("delete child reservation: %v", err)
		}

		// CR controller detects the missing slot and recreates it.
		env.reconcileCR(t, cr.Name)
		// Place the new slot.
		env.reconcileChildReservations(t, cr.Name)
		// CR controller observes Ready=True on the recreated slot.
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Errorf("expected 1 reservation after recreation, got %d", len(got))
		}
		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("expected CR to be Ready=True after slot recreation")
		}
	})

	t.Run("AcceptedAt: set when CR accepted", func(t *testing.T) {
		env := newCRIntegrationEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Fatalf("expected CR to be Ready=True")
		}
		if crState.Status.AcceptedAt == nil {
			t.Errorf("expected AcceptedAt to be set on acceptance")
		}
		if crState.Status.AcceptedAmount == nil {
			t.Errorf("expected AcceptedAmount to be set on acceptance")
		} else if crState.Status.AcceptedAmount.Cmp(resource.MustParse("4Gi")) != 0 {
			t.Errorf("AcceptedAmount: want 4Gi, got %s", crState.Status.AcceptedAmount.String())
		}
	})

	t.Run("resize failure: rolls back to AcceptedAmount, prior slot preserved", func(t *testing.T) {
		// Scheduler: accepts the first placement call (initial 4 GiB slot), rejects all subsequent.
		objects := []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}
		env := newIntgEnv(t, objects, intgAcceptFirstScheduler(1))
		defer env.close()

		cr := intgCRAllowRejection("my-cr", "uuid-resize-0001", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Phase 1: accept at 4 GiB (1 slot). Uses 1 scheduler call.
		intgDriveToTerminal(t, env, []string{cr.Name})
		var crState v1alpha1.CommittedResource
		if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &crState); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Fatalf("phase 1: expected CR to be Ready=True after initial placement")
		}
		if crState.Status.AcceptedAmount == nil || crState.Status.AcceptedAmount.Cmp(resource.MustParse("4Gi")) != 0 {
			t.Fatalf("phase 1: AcceptedAmount must be 4Gi, got %v", crState.Status.AcceptedAmount)
		}

		// Phase 2: resize to 8 GiB (needs 2 slots). Scheduler has no more accepts.
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.Amount = resource.MustParse("8Gi")
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch CR to 8Gi: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}

		// CR controller: applyReservationState bumps gen on existing slot, creates 2nd slot.
		env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		// Reservation controller: existing slot echoes new ParentGeneration (no scheduler call);
		// new slot calls scheduler → rejected.
		var resList v1alpha1.ReservationList
		env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
			v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
		})
		for _, res := range resList.Items {
			resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
			env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			env.resController.Reconcile(ctx, resReq) //nolint:errcheck
		}
		// CR controller: detects 2nd slot Ready=False → rollbackToAccepted (keeps 1 slot) → Rejected.
		env.crController.Reconcile(ctx, crReq) //nolint:errcheck

		// Rollback must preserve 1 slot (matching AcceptedAmount=4Gi), not delete all.
		var finalList v1alpha1.ReservationList
		if err := env.k8sClient.List(ctx, &finalList, client.MatchingLabels{
			v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
		}); err != nil {
			t.Fatalf("list reservations: %v", err)
		}
		if len(finalList.Items) != 1 {
			t.Errorf("resize rollback: want 1 slot (AcceptedAmount), got %d", len(finalList.Items))
		}
		intgAssertCRCondition(t, env.k8sClient, []string{cr.Name}, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)
	})

	t.Run("AllowRejection=false: eventually accepted after scheduler starts accepting", func(t *testing.T) {
		// Scheduler rejects the first 2 calls (one per reservation controller reconcile pair),
		// then accepts all subsequent. AllowRejection=false means the CR controller retries rather
		// than rejecting, so the CR must eventually reach Accepted once the scheduler cooperates.
		objects := []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}
		env := newIntgEnv(t, objects, intgRejectFirstScheduler(2))
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		// AllowRejection stays false (default), so placement failure must requeue, not reject.
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
		for range 3 {
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
			var resList v1alpha1.ReservationList
			env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			})
			for _, res := range resList.Items {
				resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			}
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		}

		var final v1alpha1.CommittedResource
		if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, &final); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		cond := meta.FindStatusCondition(final.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			t.Fatalf("no Ready condition after retries")
		}
		if cond.Reason == v1alpha1.CommittedResourceReasonRejected {
			t.Errorf("AllowRejection=false: CR must not be Rejected, got Reason=%s", cond.Reason)
		}
		if cond.Status != metav1.ConditionTrue || cond.Reason != v1alpha1.CommittedResourceReasonAccepted {
			t.Errorf("AllowRejection=false: expected Ready=True/Accepted after retries, got Ready=%s/Reason=%s", cond.Status, cond.Reason)
		}
	})
}

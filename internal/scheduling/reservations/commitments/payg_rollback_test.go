// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCRLifecycle_PAYGRollback verifies that a pre-allocated PAYG reservation slot survives
// rollback after a failed resize attempt.
//
// Scenario:
//  1. CR accepted at 4 GiB via PAYG pre-allocation (no scheduler call needed).
//  2. CR resized to 8 GiB; 2nd slot (blind scheduler path) is rejected.
//  3. Controller rolls back to AcceptedSpec: original pre-allocated slot preserved intact.
func TestCRLifecycle_PAYGRollback(t *testing.T) {
	objects := []client.Object{
		newTestFlavorKnowledge(),
		intgHypervisorWithAZ("host-1", "test-az", "vm-payg-rollback"),
	}
	env := newIntgEnv(t, objects, intgRejectScheduler, &fakeVMSource{vms: []reservations.VM{{
		UUID:              "vm-payg-rollback",
		FlavorName:        "test-flavor",
		CurrentHypervisor: "host-1",
	}}})
	defer env.close()

	cr := intgCRAllowRejection("my-cr", "uuid-payg-rollback-0001", v1alpha1.CommitmentStatusConfirmed)
	if err := env.k8sClient.Create(context.Background(), cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Phase 1: PAYG pre-allocation → Accepted (scheduler rejects but isn't called for PAYG slot).
	intgDriveToTerminal(t, env, []string{cr.Name})
	crState := env.getCR(t, cr.Name)
	if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
		t.Fatalf("phase 1: expected CR to be Ready=True after PAYG pre-allocation")
	}

	slots := env.listChildReservations(t, cr.Name)
	if len(slots) != 1 {
		t.Fatalf("phase 1: want 1 pre-allocated slot, got %d", len(slots))
	}
	if slots[0].Spec.TargetHost != "host-1" {
		t.Errorf("phase 1: expected pre-allocated slot on host-1, got %q", slots[0].Spec.TargetHost)
	}
	if _, ok := slots[0].Spec.CommittedResourceReservation.Allocations["vm-payg-rollback"]; !ok {
		t.Error("phase 1: expected vm-payg-rollback in Spec.Allocations")
	}
	preAllocatedSlotName := slots[0].Name

	// Phase 2: resize to 8 GiB (2 slots required). PAYG VM already allocated → 2nd slot goes to
	// blind scheduler path → rejected.
	patch := client.MergeFrom(crState.DeepCopy())
	crState.Spec.Amount = resource.MustParse("8Gi")
	if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
		t.Fatalf("patch CR to 8Gi: %v", err)
	}

	ctx := context.Background()
	crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}

	if _, err := env.crController.Reconcile(ctx, crReq); err != nil {
		t.Fatalf("phase 2 CR reconcile: %v", err)
	}
	var resList v1alpha1.ReservationList
	if err := env.k8sClient.List(ctx, &resList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	for _, res := range resList.Items {
		resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
		if _, err := env.resController.Reconcile(ctx, resReq); err != nil {
			t.Fatalf("reservation reconcile %s (pass 1): %v", res.Name, err)
		}
		if _, err := env.resController.Reconcile(ctx, resReq); err != nil {
			t.Fatalf("reservation reconcile %s (pass 2): %v", res.Name, err)
		}
	}
	// CR controller: 2nd slot Ready=False → rollbackToAccepted → Rejected.
	if _, err := env.crController.Reconcile(ctx, crReq); err != nil {
		t.Fatalf("phase 2 final CR reconcile: %v", err)
	}

	// Rollback must preserve exactly the original pre-allocated slot.
	var finalList v1alpha1.ReservationList
	if err := env.k8sClient.List(ctx, &finalList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations after rollback: %v", err)
	}
	if len(finalList.Items) != 1 {
		t.Fatalf("rollback: want 1 slot, got %d", len(finalList.Items))
	}
	surviving := finalList.Items[0]
	if surviving.Name != preAllocatedSlotName {
		t.Errorf("rollback: expected original pre-allocated slot %q to survive, got %q", preAllocatedSlotName, surviving.Name)
	}
	if surviving.Spec.TargetHost != "host-1" {
		t.Errorf("rollback: expected TargetHost host-1, got %q", surviving.Spec.TargetHost)
	}
	if _, ok := surviving.Spec.CommittedResourceReservation.Allocations["vm-payg-rollback"]; !ok {
		t.Error("rollback: expected vm-payg-rollback in Spec.Allocations of surviving slot")
	}
	intgAssertCRCondition(t, env.k8sClient, []string{cr.Name}, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)
}

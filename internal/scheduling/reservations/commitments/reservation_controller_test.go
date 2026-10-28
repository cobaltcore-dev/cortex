// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// mockDomainResolver is a test double for DomainResolver.
type mockDomainResolver struct {
	// names maps domainID → name returned on success.
	names map[string]string
	// err is returned for all calls when non-nil.
	err error
	// calls records every domainID passed to ResolveDomainName.
	calls []string
}

func (m *mockDomainResolver) ResolveDomainName(_ context.Context, domainID string) (string, error) {
	m.calls = append(m.calls, domainID)
	if m.err != nil {
		return "", m.err
	}
	return m.names[domainID], nil
}

func TestCommitmentReservationController_Reconcile(t *testing.T) {
	scheme := newCRTestScheme(t)

	tests := []struct {
		name          string
		reservation   *v1alpha1.Reservation
		expectedReady bool
		expectedError string
		shouldRequeue bool
	}{
		{
			name: "expect already active reservation",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID:    "test-project",
						ResourceName: "test-flavor",
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "host-1",
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReservationConditionReady,
							Status: metav1.ConditionTrue,
							Reason: "ReservationActive",
						},
					},
				},
			},
			expectedReady: true,
			shouldRequeue: true,
		},
		{
			name: "skip reservation without resource name",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{},
			},
			expectedReady: false,
			expectedError: "reservation has no resource name",
			shouldRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := newCRTestClient(scheme, tt.reservation)

			reconciler := &CommitmentReservationController{
				Client: k8sClient,
				Scheme: scheme,
				Conf: ReservationControllerConfig{
					RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
				},
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.reservation.Name,
				},
			}

			result, err := reconciler.Reconcile(context.Background(), req)

			if err != nil {
				t.Errorf("Reconcile() error = %v", err)
				return
			}

			if tt.shouldRequeue && result.RequeueAfter == 0 {
				t.Errorf("Expected requeue but got none")
			}

			if !tt.shouldRequeue && result.RequeueAfter > 0 {
				t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
			}

			var updated v1alpha1.Reservation
			err = k8sClient.Get(context.Background(), req.NamespacedName, &updated)
			if err != nil {
				t.Errorf("Failed to get updated reservation: %v", err)
				return
			}

			isReady := meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
			if isReady != tt.expectedReady {
				t.Errorf("Expected ready=%v, got ready=%v", tt.expectedReady, isReady)
			}

			if tt.expectedError != "" {
				cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
				if cond == nil || cond.Status != metav1.ConditionFalse {
					t.Errorf("Expected Ready=False with error, got %v", updated.Status.Conditions)
				}
			}
		})
	}
}

// ============================================================================
// Test: reconcileAllocations
// ============================================================================

func TestReconcileAllocations_HypervisorCRDPath(t *testing.T) {
	scheme := newCRTestScheme(t)

	now := time.Now()
	oldTime := metav1.NewTime(now.Add(-30 * time.Minute))

	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	tests := []struct {
		name                      string
		reservation               *v1alpha1.Reservation
		hypervisor                *hv1.Hypervisor
		expectedStatusAllocations map[string]string
	}{
		{
			name: "confirmed VM found on hypervisor CRD",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			hypervisor: newTestHypervisorCRD("host-1", []hv1.Instance{
				{ID: "vm-1", Name: "vm-1", Active: true},
			}),
			expectedStatusAllocations: map[string]string{"vm-1": "host-1"},
		},
		{
			name: "confirmed inactive VM still counted (stopped/shelved)",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-stopped": oldTime,
			}),
			hypervisor: newTestHypervisorCRD("host-1", []hv1.Instance{
				{ID: "vm-stopped", Name: "vm-stopped", Active: false},
			}),
			expectedStatusAllocations: map[string]string{"vm-stopped": "host-1"},
		},
		{
			name: "confirmed VM not on hypervisor CRD (stale, removed)",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			hypervisor:                newTestHypervisorCRD("host-1", []hv1.Instance{}),
			expectedStatusAllocations: map[string]string{},
		},
		{
			name:                      "empty allocations - no work to do",
			reservation:               newTestCRReservation(map[string]metav1.Time{}),
			expectedStatusAllocations: map[string]string{},
		},
		{
			name: "hypervisor CRD not found - confirmed VM removed",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			expectedStatusAllocations: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.reservation}
			if tt.hypervisor != nil {
				objects = append(objects, tt.hypervisor)
			}

			k8sClient := newCRTestClient(scheme, objects...)

			controller := &CommitmentReservationController{
				Client: k8sClient,
				Scheme: scheme,
				Conf:   config,
			}

			ctx := WithNewGlobalRequestID(context.Background())
			_, err := controller.reconcileAllocations(ctx, tt.reservation)
			if err != nil {
				t.Fatalf("reconcileAllocations() error = %v", err)
			}

			// Re-fetch reservation to check updates
			var updated v1alpha1.Reservation
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tt.reservation), &updated); err != nil {
				t.Fatalf("failed to get updated reservation: %v", err)
			}

			// Check status allocations
			actualStatusAllocs := map[string]v1alpha1.CommittedResourceAllocation{}
			if updated.Status.CommittedResourceReservation != nil {
				actualStatusAllocs = updated.Status.CommittedResourceReservation.Allocations
			}

			if len(actualStatusAllocs) != len(tt.expectedStatusAllocations) {
				t.Errorf("expected %d status allocations, got %d: %v",
					len(tt.expectedStatusAllocations), len(actualStatusAllocs), actualStatusAllocs)
			}

			for vmUUID := range tt.expectedStatusAllocations {
				if _, ok := actualStatusAllocs[vmUUID]; !ok {
					t.Errorf("expected VM %s in status allocations", vmUUID)
				}
			}

			// Check spec allocations if expected set is specified
		})
	}
}

// TestReconcileAllocations_ConfirmedVMDeparture verifies that a VM already confirmed in
// Status.Allocations is removed immediately when it disappears from the HV CRD.
func TestReconcileAllocations_ConfirmedVMDeparture(t *testing.T) {
	scheme := newCRTestScheme(t)
	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "test-reservation"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			Conditions: []metav1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: metav1.ConditionTrue, Reason: "ReservationActive"},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				// VM was previously confirmed — it is in Status.Allocations.
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-confirmed": {ObjectRef: corev1.ObjectReference{Name: "vm-confirmed"}},
				},
			},
		},
	}

	// HV CRD exists but the VM is gone — simulates termination/evacuation.
	hv := newTestHypervisorCRD("host-1", []hv1.Instance{})

	k8sClient := newCRTestClient(scheme, res, hv)
	controller := &CommitmentReservationController{Client: k8sClient, Scheme: scheme, Conf: config}

	ctx := WithNewGlobalRequestID(context.Background())
	result, err := controller.reconcileAllocations(ctx, res)
	if err != nil {
		t.Fatalf("reconcileAllocations() error = %v", err)
	}

	// Must not be treated as grace-period — departure of confirmed VM is immediate.
	if result.HasAllocationsInGracePeriod {
		t.Error("confirmed VM departure must not trigger grace period requeue")
	}

	var updated v1alpha1.Reservation
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(res), &updated); err != nil {
		t.Fatalf("get updated reservation: %v", err)
	}

	// Status.Allocations must be empty — VM removed.
	if updated.Status.CommittedResourceReservation == nil ||
		len(updated.Status.CommittedResourceReservation.Allocations) != 0 {
		t.Errorf("expected 0 status allocations after departure, got %#v",
			updated.Status.CommittedResourceReservation)
	}
}

// newTestCRReservation creates a test CR reservation with VMs confirmed in status.allocations.
// The timestamp parameter is kept for signature compatibility but is no longer used.
func newTestCRReservation(allocations map[string]metav1.Time) *v1alpha1.Reservation {
	const host = "host-1"

	statusAllocs := make(map[string]v1alpha1.CommittedResourceAllocation, len(allocations))
	for vmUUID := range allocations {
		statusAllocs[vmUUID] = v1alpha1.CommittedResourceAllocation{
			ObjectRef: corev1.ObjectReference{Name: vmUUID},
		}
	}

	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: statusAllocs,
			},
		},
	}
}

// newTestHypervisorCRD creates a test Hypervisor CRD with instances.
//

func newTestHypervisorCRD(name string, instances []hv1.Instance) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Instances: instances,
		},
	}
}

// ============================================================================
// Test: hypervisorToReservations mapper
// ============================================================================

// TestHypervisorToReservations tests the mapper that translates a Hypervisor change
// into reconcile requests for the CR reservations assigned to that host.
// This covers the mapper logic; the watch wiring itself (informer → mapper → enqueue)
// is controller-runtime's responsibility and is not unit-testable without envtest.
func TestHypervisorToReservations(t *testing.T) {
	scheme := newCRTestScheme(t)

	res1 := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-1"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}
	res2 := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-1b"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}
	resOtherHost := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-2"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-2"},
	}
	resNoHost := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-no-host"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
	}
	resFailover := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-failover"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}

	k8sClient := newCRTestClient(scheme, res1, res2, resOtherHost, resNoHost, resFailover)

	controller := &CommitmentReservationController{Client: k8sClient}

	hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
	requests := controller.hypervisorToReservations(context.Background(), hv)

	// Only CR reservations on host-1 should be enqueued; failover and other-host excluded
	got := make(map[string]bool, len(requests))
	for _, req := range requests {
		got[req.Name] = true
	}
	if len(got) != 2 {
		t.Errorf("expected 2 requests, got %d: %v", len(got), got)
	}
	for _, name := range []string{"res-host-1", "res-host-1b"} {
		if !got[name] {
			t.Errorf("expected %s in requests", name)
		}
	}
}

// ============================================================================
// Test: reconcileAllocations - candidate cleanup (now via status.allocations)
// ============================================================================

// TestReconcileAllocations_ConfirmTriggersCandidateCleanup verifies that reconcileAllocations
// maintains status.allocations for a VM already confirmed on the winning reservation.
// Candidate cleanup via spec.allocations is no longer needed since spec.allocations is deprecated.
func TestReconcileAllocations_ConfirmTriggersCandidateCleanup(t *testing.T) {
	scheme := newCRTestScheme(t)

	const vmUUID = "vm-confirmed-uuid"
	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	// Winning reservation already has the VM confirmed in status.allocations.
	winning := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-winning"},
		Spec: v1alpha1.ReservationSpec{
			Type:                         v1alpha1.ReservationTypeCommittedResource,
			TargetHost:                   "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					vmUUID: {ObjectRef: corev1.ObjectReference{Name: vmUUID}},
				},
			},
		},
	}
	hypervisor := newTestHypervisorCRD("host-1", []hv1.Instance{
		{ID: vmUUID, Name: "vm-name", Active: true},
	})

	k8sClient := newCRTestClient(scheme, winning, hypervisor)
	controller := &CommitmentReservationController{Client: k8sClient, Scheme: scheme, Conf: config}
	ctx := WithNewGlobalRequestID(context.Background())

	if _, err := controller.reconcileAllocations(ctx, winning); err != nil {
		t.Fatalf("reconcileAllocations() error = %v", err)
	}

	// VM must remain confirmed on winning reservation
	var updatedWinning v1alpha1.Reservation
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(winning), &updatedWinning); err != nil {
		t.Fatalf("failed to get winning reservation: %v", err)
	}
	if updatedWinning.Status.CommittedResourceReservation == nil {
		t.Errorf("expected vm %s confirmed in winning reservation status", vmUUID)
	} else if _, ok := updatedWinning.Status.CommittedResourceReservation.Allocations[vmUUID]; !ok {
		t.Errorf("expected vm %s confirmed in winning reservation status", vmUUID)
	}
}

// TestReconcileAllocations_NoCleanupForAlreadyConfirmedVM verifies that re-reconciling
// a reservation with an already-confirmed VM is idempotent.
func TestReconcileAllocations_NoCleanupForAlreadyConfirmedVM(t *testing.T) {
	scheme := newCRTestScheme(t)

	const vmUUID = "vm-already-confirmed"
	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	// Reservation already has the VM confirmed in Status.Allocations from a prior cycle.
	winning := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-winning"},
		Spec: v1alpha1.ReservationSpec{
			Type:                         v1alpha1.ReservationTypeCommittedResource,
			TargetHost:                   "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					vmUUID: {ObjectRef: corev1.ObjectReference{Name: vmUUID}},
				},
			},
		},
	}
	hypervisor := newTestHypervisorCRD("host-1", []hv1.Instance{
		{ID: vmUUID, Name: "vm-name", Active: true},
	})

	k8sClient := newCRTestClient(scheme, winning, hypervisor)
	controller := &CommitmentReservationController{Client: k8sClient, Scheme: scheme, Conf: config}
	ctx := WithNewGlobalRequestID(context.Background())

	if _, err := controller.reconcileAllocations(ctx, winning); err != nil {
		t.Fatalf("reconcileAllocations() error = %v", err)
	}

	// Re-reconcile must be idempotent — VM stays confirmed.
	var updated v1alpha1.Reservation
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(winning), &updated); err != nil {
		t.Fatalf("failed to get reservation: %v", err)
	}
	if updated.Status.CommittedResourceReservation == nil {
		t.Errorf("expected vm %s to remain confirmed on host-1 after re-reconcile", vmUUID)
	} else if _, ok := updated.Status.CommittedResourceReservation.Allocations[vmUUID]; !ok {
		t.Errorf("expected vm %s to remain confirmed on host-1 after re-reconcile", vmUUID)
	}
}

func TestCommitmentReservationController_reconcileInstanceReservation_Success(t *testing.T) {
	scheme := newCRTestScheme(t)

	reservation := &v1alpha1.Reservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
			},
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("4Gi"),
				hv1.ResourceCPU:    resource.MustParse("2"),
			},
		},
	}

	hypervisor1 := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-1"}}
	hypervisor2 := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-2"}}

	k8sClient := newCRTestClient(scheme, reservation, newTestFlavorKnowledge(), hypervisor1, hypervisor2)

	// Create a mock server that returns a successful response
	mockResponse := &schedulerdelegationapi.ExternalSchedulerResponse{
		Hosts: []string{"test-host-1", "test-host-2"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request body
		var req schedulerdelegationapi.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify request structure
		if req.Spec.Data.NumInstances != 1 {
			t.Errorf("Expected NumInstances to be 1, got %d", req.Spec.Data.NumInstances)
		}

		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	config := ReservationControllerConfig{
		SchedulerURL: server.URL,
	}

	reconciler := &CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   config,
	}

	// Initialize the reconciler (this sets up SchedulerClient)
	if err := reconciler.Init(context.Background(), config); err != nil {
		t.Fatalf("Failed to initialize reconciler: %v", err)
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: reservation.Name,
		},
	}

	// First reconcile: schedules the reservation and sets Spec.TargetHost
	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("First reconcile error = %v", err)
		return
	}
	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue after first reconcile but got %v", result.RequeueAfter)
	}

	// Verify Spec.TargetHost is set after first reconcile
	var afterFirstReconcile v1alpha1.Reservation
	if err = k8sClient.Get(context.Background(), req.NamespacedName, &afterFirstReconcile); err != nil {
		t.Errorf("Failed to get reservation after first reconcile: %v", err)
		return
	}
	if afterFirstReconcile.Spec.TargetHost != "test-host-1" {
		t.Errorf("Expected Spec.TargetHost=%v after first reconcile, got %v", "test-host-1", afterFirstReconcile.Spec.TargetHost)
	}

	// Second reconcile: syncs Spec.TargetHost to Status and sets Ready=True
	result, err = reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Second reconcile error = %v", err)
		return
	}
	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue after second reconcile but got %v", result.RequeueAfter)
	}

	// Verify the reservation status after second reconcile
	var updated v1alpha1.Reservation
	if err = k8sClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	if !meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionReady) {
		t.Errorf("Expected Ready=True, got %v", updated.Status.Conditions)
	}

	if updated.Status.Host != "test-host-1" {
		t.Errorf("Expected host %v, got %v", "test-host-1", updated.Status.Host)
	}
}

// ============================================================================
// Test: domain_name scheduler hint
// ============================================================================

// newTestSchedulerServer captures the decoded request and returns a single host.
func newTestSchedulerServer(t *testing.T, captured *schedulerdelegationapi.ExternalSchedulerRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
			t.Errorf("failed to decode scheduler request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{"test-host-1"}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write scheduler response: %v", err)
		}
	}))
}

func TestCommitmentReservationController_DomainNameHint(t *testing.T) {
	tests := []struct {
		name             string
		resolver         DomainResolver
		wantDomainName   string // empty means hint must be absent
		wantResolverCall string // expected domainID passed to resolver; empty means no call expected
	}{
		{
			name:             "hint present when resolver succeeds",
			resolver:         &mockDomainResolver{names: map[string]string{"domain-uuid-1": "monsoon3"}},
			wantDomainName:   "monsoon3",
			wantResolverCall: "domain-uuid-1",
		},
		{
			name:           "hint absent when resolver fails, scheduling continues",
			resolver:       &mockDomainResolver{err: errors.New("keystone unavailable")},
			wantDomainName: "",
		},
		{
			name:           "hint absent when resolver is nil (keystoneSecretRef not configured)",
			resolver:       nil,
			wantDomainName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			reservation := &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{Name: "test-reservation"},
				Spec: v1alpha1.ReservationSpec{
					Type: v1alpha1.ReservationTypeCommittedResource,
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID:    "test-project",
						DomainID:     "domain-uuid-1",
						ResourceName: "test-flavor",
					},
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: resource.MustParse("4Gi"),
						hv1.ResourceCPU:    resource.MustParse("2"),
					},
				},
			}
			k8sClient := newCRTestClient(scheme, reservation, newTestFlavorKnowledge(),
				&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-1"}})

			var captured schedulerdelegationapi.ExternalSchedulerRequest
			server := newTestSchedulerServer(t, &captured)
			defer server.Close()

			reconciler := &CommitmentReservationController{
				Client:          k8sClient,
				Scheme:          scheme,
				Conf:            ReservationControllerConfig{SchedulerURL: server.URL},
				SchedulerClient: reservations.NewSchedulerClient(server.URL),
				DomainResolver:  tt.resolver,
			}

			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: reservation.Name},
			}); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}

			// Verify resolver was called correctly.
			if tt.wantResolverCall != "" {
				m := tt.resolver.(*mockDomainResolver)
				if len(m.calls) != 1 || m.calls[0] != tt.wantResolverCall {
					t.Errorf("expected resolver called with %q, got %v", tt.wantResolverCall, m.calls)
				}
			}

			// Verify domain_name hint.
			gotDomain, err := captured.Spec.Data.GetSchedulerHintStr("domain_name")
			if tt.wantDomainName == "" {
				if err == nil {
					t.Errorf("expected domain_name hint absent, got %q", gotDomain)
				}
			} else {
				if err != nil {
					t.Fatalf("expected domain_name hint %q, got error: %v", tt.wantDomainName, err)
				}
				if gotDomain != tt.wantDomainName {
					t.Errorf("expected domain_name hint %q, got %q", tt.wantDomainName, gotDomain)
				}
			}

			// _nova_check_type must always be present regardless of resolver outcome.
			gotIntent, err := captured.Spec.Data.GetSchedulerHintStr("_nova_check_type")
			if err != nil || gotIntent != string(schedulerdelegationapi.ReserveForCommittedResourceIntent) {
				t.Errorf("expected _nova_check_type=%q, got %q (err=%v)", schedulerdelegationapi.ReserveForCommittedResourceIntent, gotIntent, err)
			}
		})
	}
}

// ============================================================================
// Tests: live migration detection in reconcileAllocations
// ============================================================================

// newHVWithCapacity creates a Hypervisor CRD with the given instances and effective capacity.
func newHVWithCapacity(name string, memGiB, cpuCores int64, instances []hv1.Instance) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memGiB)),
				hv1.ResourceCPU:    resource.MustParse(strconv.FormatInt(cpuCores, 10)),
			},
			Instances: instances,
		},
	}
}

// newConfirmedCRReservation creates a ready CR reservation with one confirmed VM on host.
func newConfirmedCRReservation(name, host, vmUUID string, slotMemGiB, slotCPU int64) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", slotMemGiB)),
				hv1.ResourceCPU:    resource.MustParse(strconv.FormatInt(slotCPU, 10)),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			Conditions: []metav1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: metav1.ConditionTrue, Reason: "ReservationActive"},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					vmUUID: {
						ObjectRef: corev1.ObjectReference{Name: vmUUID},
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", slotMemGiB)),
							hv1.ResourceCPU:    resource.MustParse(strconv.FormatInt(slotCPU, 10)),
						},
					},
				},
			},
		},
	}
}

func TestReconcileAllocations_LiveMigration(t *testing.T) {
	const (
		vmUUID  = "vm-uuid"
		vm2UUID = "vm-uuid-2"
		oldHost = "host-old"
		newHost = "host-new"
	)

	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	tests := []struct {
		name string
		// reservation to use; nil uses the default single-VM reservation
		reservation *v1alpha1.Reservation
		// extra objects beyond the base reservation and old host HV
		extraObjects []client.Object
		// expected outcomes after first reconcile pass
		wantTargetHost string
		wantStatusHost string // expected in Status.Allocations[vmUUID]; "" means absent
		// if true, run a second reconcile pass and assert state is stable
		assertSecondPass bool
	}{
		{
			name: "single VM, new host has capacity: follow the VM",
			extraObjects: []client.Object{
				newHVWithCapacity(newHost, 960, 80, []hv1.Instance{{ID: vmUUID, Active: true}}),
			},
			wantTargetHost:   newHost,
			wantStatusHost:   newHost,
			assertSecondPass: true,
		},
		{
			name: "single VM, new host at capacity: remove VM, slot stays on old host",
			extraObjects: []client.Object{
				// VM (240Gi/20) running on newHost; a full-slot blocker leaves no room for
				// the 240Gi/20 slot remainder.
				newHVWithCapacity(newHost, 480, 40, []hv1.Instance{{ID: vmUUID, Active: true}}),
				&v1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{Name: "res-blocker"},
					Spec: v1alpha1.ReservationSpec{
						Type:       v1alpha1.ReservationTypeCommittedResource,
						TargetHost: newHost,
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: resource.MustParse("480Gi"),
							hv1.ResourceCPU:    resource.MustParse("40"),
						},
					},
					Status: v1alpha1.ReservationStatus{Host: newHost},
				},
			},
			wantTargetHost: oldHost,
			wantStatusHost: "",
		},
		{
			name: "multiple VMs, one migrated: remove migrated VM, never update TargetHost",
			reservation: func() *v1alpha1.Reservation {
				res := newConfirmedCRReservation("res-1", oldHost, vmUUID, 480, 40)
				// Add a second VM confirmed on oldHost (status only).
				res.Status.CommittedResourceReservation.Allocations[vm2UUID] = v1alpha1.CommittedResourceAllocation{
					ObjectRef: corev1.ObjectReference{Name: vm2UUID},
				}
				return res
			}(),
			extraObjects: []client.Object{
				// vmUUID has migrated to newHost with plenty of capacity; vm2UUID stays on oldHost.
				// Supply oldHost HV explicitly so vm2UUID is present on it.
				newTestHypervisorCRD(oldHost, []hv1.Instance{{ID: vm2UUID, Active: true}}),
				newHVWithCapacity(newHost, 960, 80, []hv1.Instance{{ID: vmUUID, Active: true}}),
			},
			wantTargetHost: oldHost,
			wantStatusHost: "",
		},
		{
			name:           "VM gone from all hosts: remove allocation",
			extraObjects:   []client.Object{newTestHypervisorCRD("host-other", []hv1.Instance{{ID: "other-vm"}})},
			wantTargetHost: oldHost,
			wantStatusHost: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)

			res := tt.reservation
			if res == nil {
				res = newConfirmedCRReservation("res-1", oldHost, vmUUID, 480, 40)
			}

			var objects []client.Object
			objects = append(objects, res)

			// Add an oldHost HV unless the test provides its own via extraObjects.
			addsOldHostHV := false
			for _, obj := range tt.extraObjects {
				if hv, ok := obj.(*hv1.Hypervisor); ok && hv.Name == oldHost {
					addsOldHostHV = true
					break
				}
			}
			if !addsOldHostHV {
				objects = append(objects, newTestHypervisorCRD(oldHost, []hv1.Instance{}))
			}
			objects = append(objects, tt.extraObjects...)

			k8sClient := newCRTestClient(scheme, objects...)
			controller := &CommitmentReservationController{Client: k8sClient, Scheme: scheme, Conf: config}
			ctx := WithNewGlobalRequestID(context.Background())

			if _, err := controller.reconcileAllocations(ctx, res); err != nil {
				t.Fatalf("reconcileAllocations() error = %v", err)
			}

			var updated v1alpha1.Reservation
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(res), &updated); err != nil {
				t.Fatalf("failed to get updated reservation: %v", err)
			}

			if updated.Spec.TargetHost != tt.wantTargetHost {
				t.Errorf("Spec.TargetHost = %q, want %q", updated.Spec.TargetHost, tt.wantTargetHost)
			}
			var allocPresent bool
			if updated.Status.CommittedResourceReservation != nil {
				_, allocPresent = updated.Status.CommittedResourceReservation.Allocations[vmUUID]
			}
			wantPresent := tt.wantStatusHost != ""
			if allocPresent != wantPresent {
				if wantPresent {
					t.Errorf("Status.Allocations[%s] missing, want present", vmUUID)
				} else {
					t.Errorf("Status.Allocations[%s] present, want absent", vmUUID)
				}
			}

			// For the migration-follow case: run a second reconcile to confirm state is
			// stable and Status.Host was advanced to the new host in the first pass.
			if tt.assertSecondPass {
				if _, err := controller.reconcileAllocations(ctx, &updated); err != nil {
					t.Fatalf("second reconcileAllocations() error = %v", err)
				}
				var updated2 v1alpha1.Reservation
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(res), &updated2); err != nil {
					t.Fatalf("failed to get reservation after second pass: %v", err)
				}
				if updated2.Spec.TargetHost != tt.wantTargetHost {
					t.Errorf("second pass: Spec.TargetHost = %q, want %q", updated2.Spec.TargetHost, tt.wantTargetHost)
				}
				if updated2.Status.Host != tt.wantTargetHost {
					t.Errorf("second pass: Status.Host = %q, want %q", updated2.Status.Host, tt.wantTargetHost)
				}
				var allocPresent2 bool
				if updated2.Status.CommittedResourceReservation != nil {
					_, allocPresent2 = updated2.Status.CommittedResourceReservation.Allocations[vmUUID]
				}
				if allocPresent2 != (tt.wantStatusHost != "") {
					if tt.wantStatusHost != "" {
						t.Errorf("second pass: Status.Allocations[%s] missing, want present", vmUUID)
					} else {
						t.Errorf("second pass: Status.Allocations[%s] present, want absent", vmUUID)
					}
				}
			}
		})
	}
}

func TestHvCapacityChangePredicate(t *testing.T) {
	gib := func(n int64) resource.Quantity { return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI) }
	cpu := func(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

	makeHV := func(memGiB, cpuCores int64, instances []hv1.Instance) hv1.Hypervisor {
		return hv1.Hypervisor{
			Status: hv1.HypervisorStatus{
				EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
				Instances: instances,
			},
		}
	}

	tests := []struct {
		name     string
		old      hv1.Hypervisor
		new      hv1.Hypervisor
		wantFire bool
	}{
		{
			name:     "instances changed → fires",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(1024, 256, []hv1.Instance{{ID: "vm-1"}}),
			wantFire: true,
		},
		{
			name:     "effective capacity changed → fires",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(2048, 256, nil),
			wantFire: true,
		},
		{
			name: "allocation changed → fires",
			old:  makeHV(1024, 256, nil),
			new: func() hv1.Hypervisor {
				h := makeHV(1024, 256, nil)
				h.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}
				return h
			}(),
			wantFire: true,
		},
		{
			name:     "nothing relevant changed → does not fire",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(1024, 256, nil),
			wantFire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := tt.old
			newObj := tt.new
			got := hvCapacityChangePredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: &oldObj,
				ObjectNew: &newObj,
			})
			if got != tt.wantFire {
				t.Errorf("hvCapacityChangePredicate.UpdateFunc = %v, want %v", got, tt.wantFire)
			}
		})
	}
}

func TestReconcile_RevokesReadyWhenTargetHostCleared(t *testing.T) {
	scheme := newCRTestScheme(t)

	// Slot is Ready (Status.Host set, condition true) but Spec.TargetHost was cleared —
	// simulates the oversubscription eviction path.
	slot := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ResourceName: "test-flavor",
				ProjectID:    "test-project",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReservationConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "ReservationActive",
					LastTransitionTime: metav1.Now(),
				},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-1": {ObjectRef: corev1.ObjectReference{Name: "vm-1"}},
				},
			},
		},
	}

	k8sClient := newCRTestClient(scheme, slot)
	controller := &CommitmentReservationController{
		Client: k8sClient,
		Conf:   ReservationControllerConfig{RequeueIntervalActive: metav1.Duration{Duration: 30 * time.Minute}},
	}

	_, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "slot-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "slot-1"}, &updated); err != nil {
		t.Fatalf("failed to get updated reservation: %v", err)
	}
	if updated.Status.Host != "" {
		t.Errorf("expected Status.Host cleared, got %q", updated.Status.Host)
	}
	if updated.Status.CommittedResourceReservation != nil && len(updated.Status.CommittedResourceReservation.Allocations) != 0 {
		t.Errorf("expected Status.Allocations cleared, got %v", updated.Status.CommittedResourceReservation.Allocations)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("expected Ready=False, got %v", cond)
	}
	if cond != nil && cond.Reason != "PlacementRevoked" {
		t.Errorf("expected reason PlacementRevoked, got %q", cond.Reason)
	}
}

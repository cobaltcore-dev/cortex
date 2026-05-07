// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

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
					Type: v1alpha1.ReservationTypeCommittedResource,
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID:    "test-project",
						ResourceName: "test-flavor",
					},
				},
				Status: v1alpha1.ReservationStatus{
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
	recentTime := metav1.NewTime(now.Add(-5 * time.Minute)) // 5 minutes ago (within grace period)
	oldTime := metav1.NewTime(now.Add(-30 * time.Minute))   // 30 minutes ago (past grace period)

	config := ReservationControllerConfig{AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute}}

	tests := []struct {
		name                         string
		reservation                  *v1alpha1.Reservation
		hypervisor                   *hv1.Hypervisor
		expectedStatusAllocations    map[string]string
		expectedSpecAllocations      []string // VM UUIDs expected to remain in spec; nil means no check
		expectedHasGracePeriodAllocs bool
	}{
		{
			name: "old allocation - VM found on hypervisor CRD",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			hypervisor: newTestHypervisorCRD("host-1", []hv1.Instance{
				{ID: "vm-1", Name: "vm-1", Active: true},
			}),
			expectedStatusAllocations:    map[string]string{"vm-1": "host-1"},
			expectedSpecAllocations:      []string{"vm-1"},
			expectedHasGracePeriodAllocs: false,
		},
		{
			name: "old allocation - inactive VM still counted (stopped/shelved)",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-stopped": oldTime,
			}),
			hypervisor: newTestHypervisorCRD("host-1", []hv1.Instance{
				{ID: "vm-stopped", Name: "vm-stopped", Active: false},
			}),
			expectedStatusAllocations:    map[string]string{"vm-stopped": "host-1"},
			expectedSpecAllocations:      []string{"vm-stopped"},
			expectedHasGracePeriodAllocs: false,
		},
		{
			name: "old allocation - VM not on hypervisor CRD (stale, removed)",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			hypervisor:                   newTestHypervisorCRD("host-1", []hv1.Instance{}),
			expectedStatusAllocations:    map[string]string{},
			expectedSpecAllocations:      []string{},
			expectedHasGracePeriodAllocs: false,
		},
		{
			name: "new allocation within grace period - deferred to requeue",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": recentTime,
			}),
			expectedStatusAllocations:    map[string]string{},
			expectedSpecAllocations:      []string{"vm-1"},
			expectedHasGracePeriodAllocs: true,
		},
		{
			name: "mixed allocations - old verified via CRD, new in grace period",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-new": recentTime,
				"vm-old": oldTime,
			}),
			hypervisor: newTestHypervisorCRD("host-1", []hv1.Instance{
				{ID: "vm-old", Name: "vm-old", Active: true},
			}),
			expectedStatusAllocations:    map[string]string{"vm-old": "host-1"},
			expectedSpecAllocations:      []string{"vm-new", "vm-old"},
			expectedHasGracePeriodAllocs: true,
		},
		{
			name:                         "empty allocations - no work to do",
			reservation:                  newTestCRReservation(map[string]metav1.Time{}),
			expectedStatusAllocations:    map[string]string{},
			expectedHasGracePeriodAllocs: false,
		},
		{
			name: "hypervisor CRD not found - post-grace VM removed",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": oldTime,
			}),
			expectedStatusAllocations:    map[string]string{},
			expectedSpecAllocations:      []string{},
			expectedHasGracePeriodAllocs: false,
		},
		{
			name: "hypervisor CRD not found - grace period VM kept",
			reservation: newTestCRReservation(map[string]metav1.Time{
				"vm-1": recentTime,
			}),
			expectedStatusAllocations:    map[string]string{},
			expectedSpecAllocations:      []string{"vm-1"},
			expectedHasGracePeriodAllocs: true,
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
			result, err := controller.reconcileAllocations(ctx, tt.reservation)
			if err != nil {
				t.Fatalf("reconcileAllocations() error = %v", err)
			}

			// Check grace period result
			if result.HasAllocationsInGracePeriod != tt.expectedHasGracePeriodAllocs {
				t.Errorf("expected HasAllocationsInGracePeriod=%v, got %v",
					tt.expectedHasGracePeriodAllocs, result.HasAllocationsInGracePeriod)
			}

			// Re-fetch reservation to check updates
			var updated v1alpha1.Reservation
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tt.reservation), &updated); err != nil {
				t.Fatalf("failed to get updated reservation: %v", err)
			}

			// Check status allocations
			actualStatusAllocs := map[string]string{}
			if updated.Status.CommittedResourceReservation != nil {
				actualStatusAllocs = updated.Status.CommittedResourceReservation.Allocations
			}

			if len(actualStatusAllocs) != len(tt.expectedStatusAllocations) {
				t.Errorf("expected %d status allocations, got %d: %v",
					len(tt.expectedStatusAllocations), len(actualStatusAllocs), actualStatusAllocs)
			}

			for vmUUID, expectedHost := range tt.expectedStatusAllocations {
				if actualHost, ok := actualStatusAllocs[vmUUID]; !ok {
					t.Errorf("expected VM %s in status allocations", vmUUID)
				} else if actualHost != expectedHost {
					t.Errorf("VM %s: expected host %s, got %s", vmUUID, expectedHost, actualHost)
				}
			}

			// Check spec allocations if expected set is specified
			if tt.expectedSpecAllocations != nil {
				specAllocs := map[string]bool{}
				if updated.Spec.CommittedResourceReservation != nil {
					for vmUUID := range updated.Spec.CommittedResourceReservation.Allocations {
						specAllocs[vmUUID] = true
					}
				}
				if len(specAllocs) != len(tt.expectedSpecAllocations) {
					t.Errorf("expected %d spec allocations, got %d: %v",
						len(tt.expectedSpecAllocations), len(specAllocs), specAllocs)
				}
				for _, vmUUID := range tt.expectedSpecAllocations {
					if !specAllocs[vmUUID] {
						t.Errorf("expected VM %s in spec allocations", vmUUID)
					}
				}
			}
		})
	}
}

// newTestCRReservation creates a test CR reservation with allocations on "host-1".
func newTestCRReservation(allocations map[string]metav1.Time) *v1alpha1.Reservation {
	const host = "host-1"
	specAllocs := make(map[string]v1alpha1.CommittedResourceAllocation)
	for vmUUID, timestamp := range allocations {
		specAllocs[vmUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: timestamp,
			Resources: map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			},
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
				Allocations:  specAllocs,
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
				Allocations: make(map[string]string),
			},
		},
	}
}

// newTestHypervisorCRD creates a test Hypervisor CRD with instances.
//
//nolint:unparam // name parameter allows future test flexibility
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
// Test: reconcileInstanceReservation_Success (existing test)
// ============================================================================

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

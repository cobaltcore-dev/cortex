// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
)

func TestReservationReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name          string
		reservation   *v1alpha1.Reservation
		expectedPhase v1alpha1.ReservationStatusPhase
		expectedError string
		shouldRequeue bool
	}{
		{
			name: "skip already active reservation",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{
					Scheduler: v1alpha1.ReservationSchedulerSpec{
						CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
							ProjectID:  "test-project",
							FlavorName: "test-flavor",
						},
					},
				},
				Status: v1alpha1.ReservationStatus{
					Phase: v1alpha1.ReservationStatusPhaseActive,
				},
			},
			expectedPhase: v1alpha1.ReservationStatusPhaseActive,
			shouldRequeue: false,
		},
		{
			name: "skip unsupported reservation scheduler",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{
					Scheduler: v1alpha1.ReservationSchedulerSpec{},
				},
			},
			expectedPhase: v1alpha1.ReservationStatusPhaseFailed,
			expectedError: "reservation is not a cortex-nova reservation",
			shouldRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.reservation).
				WithStatusSubresource(&v1alpha1.Reservation{}).
				Build()

			reconciler := &ReservationReconciler{
				Client: client,
				Scheme: scheme,
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

			// Verify the reservation status if expected
			if tt.expectedPhase != "" {
				var updated v1alpha1.Reservation
				err := client.Get(context.Background(), req.NamespacedName, &updated)
				if err != nil {
					t.Errorf("Failed to get updated reservation: %v", err)
					return
				}

				if updated.Status.Phase != tt.expectedPhase {
					t.Errorf("Expected phase %v, got %v", tt.expectedPhase, updated.Status.Phase)
				}

				if tt.expectedError != "" && meta.IsStatusConditionFalse(updated.Status.Conditions, v1alpha1.ReservationConditionError) {
					t.Errorf("Expected error %v, got %v", tt.expectedError, updated.Status.Conditions)
				}
			}
		})
	}
}

func TestReservationReconciler_reconcileInstanceReservation_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	reservation := &v1alpha1.Reservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ReservationSpec{
			Scheduler: v1alpha1.ReservationSchedulerSpec{
				CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
					ProjectID:  "test-project",
					FlavorName: "test-flavor",
					FlavorExtraSpecs: map[string]string{
						"capabilities:hypervisor_type": "qemu",
					},
				},
			},
			Requests: map[string]resource.Quantity{
				"memory": resource.MustParse("1Gi"),
				"cpu":    resource.MustParse("2"),
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		Build()

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

	config := conf.Config{
		Endpoints: conf.EndpointsConfig{
			NovaExternalScheduler: server.URL,
		},
	}

	reconciler := &ReservationReconciler{
		Client: client,
		Scheme: scheme,
		Conf:   config,
		HypervisorClient: &mockHypervisorClient{
			hypervisorsToReturn: []Hypervisor{
				{
					Hostname: "test-host-1",
					Type:     "qemu",
					Service: struct {
						Host string `json:"host"`
					}{
						Host: "compute1",
					},
				},
				{
					Hostname: "test-host-2",
					Type:     "qemu",
					Service: struct {
						Host string `json:"host"`
					}{
						Host: "compute2",
					},
				},
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: reservation.Name,
		},
	}

	result, err := reconciler.Reconcile(context.Background(), req)

	if err != nil {
		t.Errorf("reconcileInstanceReservation() error = %v", err)
		return
	}

	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
	}

	// Verify the reservation status
	var updated v1alpha1.Reservation
	err = client.Get(context.Background(), req.NamespacedName, &updated)
	if err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	if updated.Status.Phase != v1alpha1.ReservationStatusPhaseActive {
		t.Errorf("Expected phase %v, got %v", v1alpha1.ReservationStatusPhaseActive, updated.Status.Phase)
	}

	if updated.Status.Host != "test-host-1" {
		t.Errorf("Expected host %v, got %v", "test-host-1", updated.Status.Host)
	}

	if meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionError) {
		t.Errorf("Expected no error, got %v", updated.Status.Conditions)
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
)

func TestComputeReservationReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name          string
		reservation   *v1alpha1.ComputeReservation
		expectedPhase v1alpha1.ComputeReservationStatusPhase
		expectedError string
		shouldRequeue bool
	}{
		{
			name: "skip already active reservation",
			reservation: &v1alpha1.ComputeReservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ComputeReservationSpec{
					Kind:      v1alpha1.ComputeReservationSpecKindInstance,
					ProjectID: "test-project",
				},
				Status: v1alpha1.ComputeReservationStatus{
					Phase: v1alpha1.ComputeReservationStatusPhaseActive,
				},
			},
			expectedPhase: v1alpha1.ComputeReservationStatusPhaseActive,
			shouldRequeue: false,
		},
		{
			name: "skip unsupported reservation kind",
			reservation: &v1alpha1.ComputeReservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ComputeReservationSpec{
					Kind:      "unsupported",
					ProjectID: "test-project",
				},
			},
			shouldRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.reservation).
				Build()

			reconciler := &ComputeReservationReconciler{
				Client: client,
				Scheme: scheme,
				Conf: Config{
					Hypervisors: []string{"kvm", "vmware"},
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

			// Verify the reservation status if expected
			if tt.expectedPhase != "" {
				var updated v1alpha1.ComputeReservation
				err := client.Get(context.Background(), req.NamespacedName, &updated)
				if err != nil {
					t.Errorf("Failed to get updated reservation: %v", err)
					return
				}

				if updated.Status.Phase != tt.expectedPhase {
					t.Errorf("Expected phase %v, got %v", tt.expectedPhase, updated.Status.Phase)
				}

				if tt.expectedError != "" && updated.Status.Error != tt.expectedError {
					t.Errorf("Expected error %v, got %v", tt.expectedError, updated.Status.Error)
				}
			}
		})
	}
}

func TestComputeReservationReconciler_reconcileInstanceReservation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name          string
		reservation   *v1alpha1.ComputeReservation
		config        Config
		mockResponse  *api.ExternalSchedulerResponse
		expectedPhase v1alpha1.ComputeReservationStatusPhase
		expectedError string
		shouldRequeue bool
	}{
		{
			name: "unsupported hypervisor type",
			reservation: &v1alpha1.ComputeReservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ComputeReservationSpec{
					Kind:      v1alpha1.ComputeReservationSpecKindInstance,
					ProjectID: "test-project",
					Instance: v1alpha1.ComputeReservationSpecInstance{
						Flavor: "test-flavor",
						ExtraSpecs: map[string]string{
							"capabilities:hypervisor_type": "unsupported",
						},
						Requests: map[string]resource.Quantity{
							"memory": resource.MustParse("1Gi"),
							"cpu":    resource.MustParse("2"),
						},
					},
				},
			},
			config: Config{
				Hypervisors: []string{"kvm", "vmware"},
			},
			expectedPhase: v1alpha1.ComputeReservationStatusPhaseFailed,
			expectedError: "unsupported hv 'unsupported', supported: kvm, vmware",
			shouldRequeue: false,
		},
		{
			name: "missing hypervisor type",
			reservation: &v1alpha1.ComputeReservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ComputeReservationSpec{
					Kind:      v1alpha1.ComputeReservationSpecKindInstance,
					ProjectID: "test-project",
					Instance: v1alpha1.ComputeReservationSpecInstance{
						Flavor:     "test-flavor",
						ExtraSpecs: map[string]string{},
						Requests: map[string]resource.Quantity{
							"memory": resource.MustParse("1Gi"),
							"cpu":    resource.MustParse("2"),
						},
					},
				},
			},
			config: Config{
				Hypervisors: []string{"kvm", "vmware"},
			},
			expectedPhase: v1alpha1.ComputeReservationStatusPhaseFailed,
			expectedError: "hypervisor type is not specified",
			shouldRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.reservation).
				WithStatusSubresource(&v1alpha1.ComputeReservation{}).
				Build()

			// Create a mock server for the external scheduler
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mockResponse != nil {
					json.NewEncoder(w).Encode(tt.mockResponse)
				} else {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()

			tt.config.Endpoints.NovaExternalScheduler = server.URL

			reconciler := &ComputeReservationReconciler{
				Client: client,
				Scheme: scheme,
				Conf:   tt.config,
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.reservation.Name,
				},
			}

			result, err := reconciler.reconcileInstanceReservation(context.Background(), req, *tt.reservation)

			if err != nil && !tt.shouldRequeue {
				t.Errorf("reconcileInstanceReservation() error = %v", err)
				return
			}

			if tt.shouldRequeue && result.RequeueAfter == 0 {
				t.Errorf("Expected requeue but got none")
			}

			if !tt.shouldRequeue && result.RequeueAfter > 0 {
				t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
			}

			// Verify the reservation status
			var updated v1alpha1.ComputeReservation
			err = client.Get(context.Background(), req.NamespacedName, &updated)
			if err != nil {
				t.Errorf("Failed to get updated reservation: %v", err)
				return
			}

			if updated.Status.Phase != tt.expectedPhase {
				t.Errorf("Expected phase %v, got %v", tt.expectedPhase, updated.Status.Phase)
			}

			if tt.expectedError != "" && updated.Status.Error != tt.expectedError {
				t.Errorf("Expected error %v, got %v", tt.expectedError, updated.Status.Error)
			}
		})
	}
}

func TestComputeReservationReconciler_reconcileInstanceReservation_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	reservation := &v1alpha1.ComputeReservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Kind:      v1alpha1.ComputeReservationSpecKindInstance,
			ProjectID: "test-project",
			Instance: v1alpha1.ComputeReservationSpecInstance{
				Flavor: "test-flavor",
				ExtraSpecs: map[string]string{
					"capabilities:hypervisor_type": "kvm",
				},
				Requests: map[string]resource.Quantity{
					"memory": resource.MustParse("1Gi"),
					"cpu":    resource.MustParse("2"),
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation).
		WithStatusSubresource(&v1alpha1.ComputeReservation{}).
		Build()

	// Create a mock server that returns a successful response
	mockResponse := &api.ExternalSchedulerResponse{
		Hosts: []string{"test-host-1", "test-host-2"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request body
		var req api.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify request structure
		if req.Pipeline != "reservations" {
			t.Errorf("Expected Pipeline to be 'reservations', got %q", req.Pipeline)
		}
		if req.Spec.Data.NumInstances != 1 {
			t.Errorf("Expected NumInstances to be 1, got %d", req.Spec.Data.NumInstances)
		}

		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	config := Config{
		Hypervisors: []string{"kvm", "vmware"},
		Endpoints: EndpointsConfig{
			NovaExternalScheduler: server.URL,
		},
	}

	reconciler := &ComputeReservationReconciler{
		Client: client,
		Scheme: scheme,
		Conf:   config,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: reservation.Name,
		},
	}

	result, err := reconciler.reconcileInstanceReservation(context.Background(), req, *reservation)

	if err != nil {
		t.Errorf("reconcileInstanceReservation() error = %v", err)
		return
	}

	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
	}

	// Verify the reservation status
	var updated v1alpha1.ComputeReservation
	err = client.Get(context.Background(), req.NamespacedName, &updated)
	if err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	if updated.Status.Phase != v1alpha1.ComputeReservationStatusPhaseActive {
		t.Errorf("Expected phase %v, got %v", v1alpha1.ComputeReservationStatusPhaseActive, updated.Status.Phase)
	}

	if updated.Status.Host != "test-host-1" {
		t.Errorf("Expected host %v, got %v", "test-host-1", updated.Status.Host)
	}

	if updated.Status.Error != "" {
		t.Errorf("Expected no error, got %v", updated.Status.Error)
	}
}

func TestComputeReservationReconciler_reconcileBareResourceReservation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	reservation := &v1alpha1.ComputeReservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Kind:      v1alpha1.ComputeReservationSpecKindBareResource,
			ProjectID: "test-project",
			BareResource: v1alpha1.ComputeReservationSpecBareResource{
				Requests: map[string]resource.Quantity{
					"memory": resource.MustParse("1Gi"),
					"cpu":    resource.MustParse("2"),
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation).
		WithStatusSubresource(&v1alpha1.ComputeReservation{}).
		Build()

	reconciler := &ComputeReservationReconciler{
		Client: client,
		Scheme: scheme,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: reservation.Name,
		},
	}

	result, err := reconciler.reconcileBareResourceReservation(context.Background(), req, *reservation)

	if err != nil {
		t.Errorf("reconcileBareResourceReservation() error = %v", err)
		return
	}

	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
	}

	// Verify the reservation status
	var updated v1alpha1.ComputeReservation
	err = client.Get(context.Background(), req.NamespacedName, &updated)
	if err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	if updated.Status.Phase != v1alpha1.ComputeReservationStatusPhaseFailed {
		t.Errorf("Expected phase %v, got %v", v1alpha1.ComputeReservationStatusPhaseFailed, updated.Status.Phase)
	}

	expectedError := "bare resource reservations are not supported"
	if updated.Status.Error != expectedError {
		t.Errorf("Expected error %v, got %v", expectedError, updated.Status.Error)
	}
}

func TestComputeReservationReconciler_SetupWithManager(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	reconciler := &ComputeReservationReconciler{
		Scheme: scheme,
	}

	// This test just verifies that SetupWithManager method exists
	// We can't easily test the actual setup without a real manager
	// but we can verify the method signature is correct by calling it with nil
	// (it will return an error, but that's expected)
	err := reconciler.SetupWithManager(nil)
	if err == nil {
		t.Error("Expected error when calling SetupWithManager with nil manager")
	}
}

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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestCommitmentReservationController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hypervisor scheme: %v", err)
	}

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
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.reservation).
				WithStatusSubresource(&v1alpha1.Reservation{}).
				Build()

			reconciler := &CommitmentReservationController{
				Client: client,
				Scheme: scheme,
				Conf: Config{
					RequeueIntervalActive: 5 * time.Minute,
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

			// Verify the reservation status
			var updated v1alpha1.Reservation
			err = client.Get(context.Background(), req.NamespacedName, &updated)
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

func TestCommitmentReservationController_reconcileInstanceReservation_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hypervisor scheme: %v", err)
	}

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
				hv1.ResourceMemory: resource.MustParse("1Gi"),
				hv1.ResourceCPU:    resource.MustParse("2"),
			},
		},
	}

	// Create flavor group knowledge CRD for the test
	flavorGroups := []struct {
		Name    string `json:"name"`
		Flavors []struct {
			Name       string            `json:"name"`
			MemoryMB   uint64            `json:"memoryMB"`
			VCPUs      uint64            `json:"vcpus"`
			ExtraSpecs map[string]string `json:"extraSpecs"`
		} `json:"flavors"`
	}{
		{
			Name: "test-group",
			Flavors: []struct {
				Name       string            `json:"name"`
				MemoryMB   uint64            `json:"memoryMB"`
				VCPUs      uint64            `json:"vcpus"`
				ExtraSpecs map[string]string `json:"extraSpecs"`
			}{
				{
					Name:       "test-flavor",
					MemoryMB:   1024,
					VCPUs:      2,
					ExtraSpecs: map[string]string{},
				},
			},
		},
	}

	// Marshal flavor groups into runtime.RawExtension
	flavorGroupsJSON, err := json.Marshal(map[string]interface{}{
		"features": flavorGroups,
	})
	if err != nil {
		t.Fatalf("Failed to marshal flavor groups: %v", err)
	}

	flavorGroupKnowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "flavor-groups",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "flavor_groups",
			},
			Recency: metav1.Duration{Duration: 0},
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw:       runtime.RawExtension{Raw: flavorGroupsJSON},
			RawLength: 1,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "TestReady",
				},
			},
		},
	}

	// Create mock hypervisors
	hypervisor1 := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-host-1",
		},
		Spec: hv1.HypervisorSpec{},
	}
	hypervisor2 := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-host-2",
		},
		Spec: hv1.HypervisorSpec{},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(reservation, flavorGroupKnowledge, hypervisor1, hypervisor2).
		WithStatusSubresource(&v1alpha1.Reservation{}, &v1alpha1.Knowledge{}).
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

	config := Config{
		NovaExternalScheduler: server.URL,
	}

	reconciler := &CommitmentReservationController{
		Client: client,
		Scheme: scheme,
		Conf:   config,
	}

	// Initialize the reconciler (this sets up SchedulerClient)
	if err := reconciler.Init(context.Background(), client, config); err != nil {
		t.Fatalf("Failed to initialize reconciler: %v", err)
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

	if !meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionReady) {
		t.Errorf("Expected Ready=True, got %v", updated.Status.Conditions)
	}

	if updated.Status.Host != "test-host-1" {
		t.Errorf("Expected host %v, got %v", "test-host-1", updated.Status.Host)
	}
}

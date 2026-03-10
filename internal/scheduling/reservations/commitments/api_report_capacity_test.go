// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/liquid"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestHandleReportCapacity(t *testing.T) {
	// Setup fake client
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create empty flavor groups knowledge so capacity calculation doesn't fail
	emptyKnowledge := createEmptyFlavorGroupKnowledge()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(emptyKnowledge).
		Build()

	api := NewAPI(fakeClient)

	tests := []struct {
		name           string
		method         string
		body           interface{}
		expectedStatus int
		checkResponse  func(*testing.T, *liquid.ServiceCapacityReport)
	}{
		{
			name:           "POST request succeeds",
			method:         http.MethodPost,
			body:           liquid.ServiceCapacityRequest{},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp *liquid.ServiceCapacityReport) {
				// Resources may be nil or empty for empty capacity
				if len(resp.Resources) != 0 {
					t.Errorf("Expected empty or nil Resources, got %d resources", len(resp.Resources))
				}
			},
		},
		{
			name:           "POST with empty body succeeds",
			method:         http.MethodPost,
			body:           nil,
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp *liquid.ServiceCapacityReport) {
				// Resources may be nil or empty for empty capacity
				if len(resp.Resources) != 0 {
					t.Errorf("Expected empty or nil Resources, got %d resources", len(resp.Resources))
				}
			},
		},
		{
			name:           "GET request fails",
			method:         http.MethodGet,
			body:           nil,
			expectedStatus: http.StatusMethodNotAllowed,
			checkResponse:  nil,
		},
		{
			name:           "PUT request fails",
			method:         http.MethodPut,
			body:           nil,
			expectedStatus: http.StatusMethodNotAllowed,
			checkResponse:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			var req *http.Request
			if tt.body != nil {
				bodyBytes, err := json.Marshal(tt.body)
				if err != nil {
					t.Fatal(err)
				}
				req = httptest.NewRequest(tt.method, "/v1/report-capacity", bytes.NewReader(bodyBytes))
			} else {
				req = httptest.NewRequest(tt.method, "/v1/report-capacity", http.NoBody)
			}
			req = req.WithContext(context.Background())

			// Create response recorder
			rr := httptest.NewRecorder()

			// Call handler
			api.HandleReportCapacity(rr, req)

			// Check status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Check response if applicable
			if tt.checkResponse != nil && rr.Code == http.StatusOK {
				var resp liquid.ServiceCapacityReport
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				tt.checkResponse(t, &resp)
			}
		})
	}
}

func TestCapacityCalculator(t *testing.T) {
	// Setup fake client with Knowledge CRD
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	t.Run("CalculateCapacity returns error when no flavor groups knowledge exists", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
		_, err := calculator.CalculateCapacity(context.Background())
		if err == nil {
			t.Fatal("Expected error when flavor groups knowledge doesn't exist, got nil")
		}
		if !strings.Contains(err.Error(), "flavor groups knowledge CRD not found") {
			t.Errorf("Expected 'flavor groups knowledge CRD not found' error, got: %v", err)
		}
	})

	t.Run("CalculateCapacity returns empty report when flavor groups knowledge exists but is empty", func(t *testing.T) {
		// Create empty flavor groups knowledge
		emptyKnowledge := createEmptyFlavorGroupKnowledge()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(emptyKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if report.Resources == nil {
			t.Error("Expected Resources map to be initialized")
		}

		if len(report.Resources) != 0 {
			t.Errorf("Expected 0 resources, got %d", len(report.Resources))
		}
	})

	t.Run("CalculateCapacity returns empty perAZ when no HostDetails exist", func(t *testing.T) {
		// Create a flavor group knowledge without host details
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(report.Resources) != 1 {
			t.Fatalf("Expected 1 resource, got %d", len(report.Resources))
		}

		resource := report.Resources[liquid.ResourceName("ram_test-group")]
		if resource == nil {
			t.Fatal("Expected ram_test-group resource to exist")
		}

		// Should have empty perAZ map when no host details
		if len(resource.PerAZ) != 0 {
			t.Errorf("Expected 0 AZs, got %d", len(resource.PerAZ))
		}
	})
}

// createEmptyFlavorGroupKnowledge creates an empty flavor groups Knowledge CRD
func createEmptyFlavorGroupKnowledge() *v1alpha1.Knowledge {
	// Box empty array properly
	emptyFeatures := []map[string]interface{}{}
	raw, err := v1alpha1.BoxFeatureList(emptyFeatures)
	if err != nil {
		panic(err) // Should never happen for empty slice
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{
			Name:      "flavor-groups",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "flavor_groups",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: "True",
				},
			},
			Raw: raw,
		},
	}
}

// createTestFlavorGroupKnowledge creates a test Knowledge CRD with flavor group data
func createTestFlavorGroupKnowledge(t *testing.T, groupName string) *v1alpha1.Knowledge {
	t.Helper()

	features := []map[string]interface{}{
		{
			"name": groupName,
			"flavors": []map[string]interface{}{
				{
					"name":     "test_c8_m32",
					"vcpus":    8,
					"memoryMB": 32768,
					"diskGB":   50,
				},
			},
			"largestFlavor": map[string]interface{}{
				"name":     "test_c8_m32",
				"vcpus":    8,
				"memoryMB": 32768,
				"diskGB":   50,
			},
		},
	}

	// Use BoxFeatureList to properly format the features
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{
			Name:      "flavor-groups",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "flavor_groups",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: "True",
				},
			},
			Raw: raw,
		},
	}
}

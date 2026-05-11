// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/liquid"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
)

func TestHandleReportCapacity(t *testing.T) {
	// Setup fake client
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// testCapacityConfig enables capacity reporting for all groups via "*" catch-all.
	testCapacityConfig := commitments.APIConfig{
		EnableReportCapacity: true,
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {
				RAM:       commitments.ResourceTypeConfig{HasCapacity: true},
				Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
				Instances: commitments.ResourceTypeConfig{HasCapacity: true},
			},
		},
	}

	// Create empty flavor groups knowledge so capacity calculation doesn't fail
	emptyKnowledge := createEmptyFlavorGroupKnowledge()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(emptyKnowledge).
		Build()

	api := NewAPIWithConfig(fakeClient, testCapacityConfig, nil)

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
				req = httptest.NewRequest(tt.method, "/commitments/v1/report-capacity", bytes.NewReader(bodyBytes))
			} else {
				req = httptest.NewRequest(tt.method, "/commitments/v1/report-capacity", http.NoBody)
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

	testCapacityConfig := commitments.APIConfig{
		FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
			"*": {
				RAM:       commitments.ResourceTypeConfig{HasCapacity: true},
				Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
				Instances: commitments.ResourceTypeConfig{HasCapacity: true},
			},
		},
	}

	t.Run("CalculateCapacity returns error when no flavor groups knowledge exists", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{
			AllAZs: []liquid.AvailabilityZone{"az-one", "az-two"},
		}
		_, err := calculator.CalculateCapacity(context.Background(), req)
		if err == nil {
			t.Fatal("Expected error when flavor groups knowledge doesn't exist, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})

	t.Run("CalculateCapacity returns empty report when flavor groups knowledge exists but is empty", func(t *testing.T) {
		// Create empty flavor groups knowledge
		emptyKnowledge := createEmptyFlavorGroupKnowledge()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(emptyKnowledge).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{
			AllAZs: []liquid.AvailabilityZone{"az-one", "az-two"},
		}
		report, err := calculator.CalculateCapacity(context.Background(), req)
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

	t.Run("CalculateCapacity returns perAZ entries for all AZs from request", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{
			AllAZs: []liquid.AvailabilityZone{"qa-de-1a", "qa-de-1b", "qa-de-1d"},
		}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(report.Resources) != 3 {
			t.Fatalf("Expected 3 resources (_ram, _cores, _instances), got %d", len(report.Resources))
		}

		// Verify all resources have exactly the requested AZs
		verifyPerAZMatchesRequest(t, report.Resources["hw_version_test-group_ram"], req.AllAZs)
		verifyPerAZMatchesRequest(t, report.Resources["hw_version_test-group_cores"], req.AllAZs)
		verifyPerAZMatchesRequest(t, report.Resources["hw_version_test-group_instances"], req.AllAZs)
	})

	t.Run("CalculateCapacity with empty AllAZs returns empty perAZ maps", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{}}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(report.Resources) != 3 {
			t.Fatalf("Expected 3 resources, got %d", len(report.Resources))
		}

		for resName, res := range report.Resources {
			if len(res.PerAZ) != 0 {
				t.Errorf("%s: expected empty PerAZ, got %d entries", resName, len(res.PerAZ))
			}
		}
	})

	t.Run("CalculateCapacity responds to different AZ sets correctly", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)

		req1 := liquid.ServiceCapacityRequest{
			AllAZs: []liquid.AvailabilityZone{"eu-de-1a", "eu-de-1b"},
		}
		report1, err := calculator.CalculateCapacity(context.Background(), req1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		req2 := liquid.ServiceCapacityRequest{
			AllAZs: []liquid.AvailabilityZone{"us-west-1a", "us-west-1b", "us-west-1c", "us-west-1d"},
		}
		report2, err := calculator.CalculateCapacity(context.Background(), req2)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify reports have exactly the requested AZs
		for _, res := range report1.Resources {
			verifyPerAZMatchesRequest(t, res, req1.AllAZs)
		}
		for _, res := range report2.Resources {
			verifyPerAZMatchesRequest(t, res, req2.AllAZs)
		}
	})

	t.Run("CalculateCapacity reads capacity and usage from Ready CRD", func(t *testing.T) {
		knowledge := createTestFlavorGroupKnowledge(t)
		crd := createTestFlavorGroupCapacity(1000, 800, true)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(knowledge, crd).
			WithStatusSubresource(crd).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ramRes := report.Resources["hw_version_test-group_ram"]
		if ramRes == nil {
			t.Fatal("expected hw_version_test-group_ram resource")
		}
		azReport := ramRes.PerAZ["az-one"]
		if azReport == nil {
			t.Fatal("expected az-one entry")
		}
		if azReport.Capacity != 1000 {
			t.Errorf("expected capacity=1000, got %d", azReport.Capacity)
		}
		if !azReport.Usage.IsSome() {
			t.Fatal("expected usage to be set for Ready CRD")
		}
		// usage = (total - placeable) slots = (1000 - 800) = 200 slots
		if usage := azReport.Usage.UnwrapOr(0); usage != 200 {
			t.Errorf("expected usage=200 (200 slots), got %d", usage)
		}
	})

	t.Run("CalculateCapacity returns zero capacity for missing CRD", func(t *testing.T) {
		knowledge := createTestFlavorGroupKnowledge(t)
		// CRD exists only for az-one; az-two has no CRD
		crd := createTestFlavorGroupCapacity(500, 400, true)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(knowledge, crd).
			WithStatusSubresource(crd).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one", "az-two"}}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ramRes := report.Resources["hw_version_test-group_ram"]
		if ramRes == nil {
			t.Fatal("expected hw_version_test-group_ram resource")
		}
		azTwo := ramRes.PerAZ["az-two"]
		if azTwo == nil {
			t.Fatal("expected az-two entry even without CRD")
		}
		if azTwo.Capacity != 0 {
			t.Errorf("expected capacity=0 for missing CRD, got %d", azTwo.Capacity)
		}
	})

	t.Run("CalculateCapacity omits usage for stale CRD (Ready=False)", func(t *testing.T) {
		knowledge := createTestFlavorGroupKnowledge(t)
		crd := createTestFlavorGroupCapacity(1000, 800, false)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(knowledge, crd).
			WithStatusSubresource(crd).
			Build()

		calculator := commitments.NewCapacityCalculator(fakeClient, testCapacityConfig)
		req := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ramRes := report.Resources["hw_version_test-group_ram"]
		if ramRes == nil {
			t.Fatal("expected hw_version_test-group_ram resource")
		}
		azReport := ramRes.PerAZ["az-one"]
		if azReport == nil {
			t.Fatal("expected az-one entry")
		}
		// Stale CRD: last-known capacity is still reported (1000 slots)
		if azReport.Capacity != 1000 {
			t.Errorf("expected last-known capacity=1000 for stale CRD, got %d", azReport.Capacity)
		}
		// Stale CRD: usage must be absent (None)
		if azReport.Usage.IsSome() {
			t.Error("expected usage to be absent (None) for stale CRD")
		}
	})

	t.Run("CalculateCapacity omits resources with HasCapacity=false", func(t *testing.T) {
		knowledge := createTestFlavorGroupKnowledge(t)
		crd := createTestFlavorGroupCapacity(100, 80, true)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(knowledge, crd).
			WithStatusSubresource(crd).
			Build()

		// Only RAM and Cores have capacity; Instances does not.
		cfgNoInstances := commitments.APIConfig{
			FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
				"*": {
					RAM:       commitments.ResourceTypeConfig{HasCapacity: true},
					Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
					Instances: commitments.ResourceTypeConfig{HasCapacity: false},
				},
			},
		}
		calculator := commitments.NewCapacityCalculator(fakeClient, cfgNoInstances)
		req := liquid.ServiceCapacityRequest{AllAZs: []liquid.AvailabilityZone{"az-one"}}
		report, err := calculator.CalculateCapacity(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(report.Resources) != 2 {
			t.Fatalf("expected 2 resources (ram, cores), got %d: %v", len(report.Resources), report.Resources)
		}
		if _, ok := report.Resources["hw_version_test-group_instances"]; ok {
			t.Error("expected hw_version_test-group_instances to be absent (HasCapacity=false)")
		}
	})
}

// This follows the same semantics as nova liquid: the response must contain
// entries for all AZs in AllAZs, no more and no less.
func verifyPerAZMatchesRequest(t *testing.T, res *liquid.ResourceCapacityReport, requestedAZs []liquid.AvailabilityZone) {
	t.Helper()
	if res == nil {
		t.Error("resource is nil")
		return
	}
	if len(res.PerAZ) != len(requestedAZs) {
		t.Errorf("expected %d AZs, got %d", len(requestedAZs), len(res.PerAZ))
	}
	for _, az := range requestedAZs {
		if _, ok := res.PerAZ[az]; !ok {
			t.Errorf("missing entry for requested AZ %s", az)
		}
	}
	for az := range res.PerAZ {
		if !slices.Contains(requestedAZs, az) {
			t.Errorf("unexpected AZ %s in response (not in request)", az)
		}
	}
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
			Name: "flavor-groups",
			// No namespace - Knowledge is cluster-scoped
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

// createTestFlavorGroupCapacity creates a FlavorGroupCapacity CRD for testing.
// totalSlots and placeableSlots are for the named smallest flavor entry.
// ready controls whether the Ready condition is True or False.
func createTestFlavorGroupCapacity(totalSlots, placeableSlots int64, ready bool) *v1alpha1.FlavorGroupCapacity {
	const group = "test-group"
	const az = "az-one"
	const smallestFlavorName = "test_c8_m32"
	conditionStatus := v1.ConditionTrue
	if !ready {
		conditionStatus = v1.ConditionFalse
	}
	return &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: v1.ObjectMeta{
			Name: group + "-" + az,
		},
		Spec: v1alpha1.FlavorGroupCapacitySpec{
			FlavorGroup:      group,
			AvailabilityZone: az,
		},
		Status: v1alpha1.FlavorGroupCapacityStatus{
			Flavors: []v1alpha1.FlavorCapacityStatus{
				{
					FlavorName:           smallestFlavorName,
					TotalCapacityVMSlots: totalSlots,
					PlaceableVMs:         placeableSlots,
				},
			},
			Conditions: []v1.Condition{
				{
					Type:   v1alpha1.FlavorGroupCapacityConditionReady,
					Status: conditionStatus,
				},
			},
		},
	}
}

// that accepts commitments (has fixed RAM/core ratio)
func createTestFlavorGroupKnowledge(t *testing.T) *v1alpha1.Knowledge {
	t.Helper()

	features := []map[string]interface{}{
		{
			"name": "test-group",
			"flavors": []map[string]interface{}{
				{
					"name":     "test_c8_m32",
					"vcpus":    8,
					"memoryMB": 32752,
					"diskGB":   50,
				},
			},
			"largestFlavor": map[string]interface{}{
				"name":     "test_c8_m32",
				"vcpus":    8,
				"memoryMB": 32752,
				"diskGB":   50,
			},
			"smallestFlavor": map[string]interface{}{
				"name":     "test_c8_m32",
				"vcpus":    8,
				"memoryMB": 32752,
				"diskGB":   50,
			},
			// Fixed RAM/core ratio (4096 MiB per vCPU) - required for group to accept commitments
			"ramCoreRatio": 4096,
		},
	}

	// Use BoxFeatureList to properly format the features
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{
			Name: "flavor-groups",
			// No namespace - Knowledge is cluster-scoped
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

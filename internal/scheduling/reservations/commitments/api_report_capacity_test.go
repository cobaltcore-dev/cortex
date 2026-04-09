// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

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

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
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

	t.Run("CalculateCapacity returns error when no flavor groups knowledge exists", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
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

		calculator := NewCapacityCalculator(fakeClient)
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
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
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
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient)
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
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient)

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
}

// verifyPerAZMatchesRequest checks that perAZ entries match exactly the requested AZs.
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

// createTestFlavorGroupKnowledge creates a test Knowledge CRD with flavor group data
// that accepts commitments (has fixed RAM/core ratio)
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
			"smallestFlavor": map[string]interface{}{
				"name":     "test_c8_m32",
				"vcpus":    8,
				"memoryMB": 32768,
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

func TestCapacityCalculatorWithScheduler(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const (
		flavorGroup = "test-group"
		az          = "az-a"
		flavorMemMB = uint64(32768)
		flavorVCPUs = uint64(8)
	)

	flavorGroupKnowledge := createTestFlavorGroupKnowledgeWithSmallest(t, flavorGroup, flavorMemMB, flavorVCPUs)
	hostDetailsKnowledge := createTestHostDetailsKnowledge(t, map[string]string{
		"host-1": az,
		"host-2": az,
	})

	t.Run("computes capacity and usage via two scheduler calls", func(t *testing.T) {
		// kvm-report-capacity returns 5 hosts (total capacity).
		// kvm-general-purpose-load-balancing-all-filters-enabled returns 3 hosts (currently available).
		// usage = 5 - 3 = 2.
		server := newPipelineMockSchedulerServer(t, map[string][]string{
			"kvm-report-capacity": {"h1", "h2", "h3", "h4", "h5"},
			"kvm-general-purpose-load-balancing-all-filters-enabled": {"h1", "h2", "h3"},
		})
		defer server.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hostDetailsKnowledge).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		res, ok := report.Resources[liquid.ResourceName("ram_"+flavorGroup)]
		if !ok {
			t.Fatal("expected ram_test-group resource")
		}
		azReport, ok := res.PerAZ[liquid.AvailabilityZone(az)]
		if !ok {
			t.Fatalf("expected %s in perAZ", az)
		}

		if azReport.Capacity != 5 {
			t.Errorf("expected capacity = 5, got %d", azReport.Capacity)
		}
		usageVal, ok := azReport.Usage.Unpack()
		if !ok {
			t.Fatal("expected usage to be set")
		}
		if usageVal != 2 {
			t.Errorf("expected usage = 2, got %d", usageVal)
		}
	})

	t.Run("usage is zero when total equals currently available", func(t *testing.T) {
		server := newPipelineMockSchedulerServer(t, map[string][]string{
			"kvm-report-capacity": {"h1", "h2"},
			"kvm-general-purpose-load-balancing-all-filters-enabled": {"h1", "h2"},
		})
		defer server.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hostDetailsKnowledge).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		azReport := report.Resources[liquid.ResourceName("ram_"+flavorGroup)].PerAZ[liquid.AvailabilityZone(az)]
		usageVal, ok := azReport.Usage.Unpack()
		if !ok {
			t.Fatal("expected usage to be set")
		}
		if usageVal != 0 {
			t.Errorf("expected usage = 0, got %d", usageVal)
		}
	})

	t.Run("usage is clamped to zero when currently available exceeds total", func(t *testing.T) {
		// Pathological: currently-available call returns more hosts than total capacity call.
		server := newPipelineMockSchedulerServer(t, map[string][]string{
			"kvm-report-capacity": {"h1"},
			"kvm-general-purpose-load-balancing-all-filters-enabled": {"h1", "h2", "h3"},
		})
		defer server.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hostDetailsKnowledge).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		azReport := report.Resources[liquid.ResourceName("ram_"+flavorGroup)].PerAZ[liquid.AvailabilityZone(az)]
		usageVal, ok := azReport.Usage.Unpack()
		if !ok {
			t.Fatal("expected usage to be set")
		}
		if usageVal != 0 {
			t.Errorf("expected usage = 0 (clamped), got %d", usageVal)
		}
	})

	t.Run("scheduler failure yields empty AZ report without aborting", func(t *testing.T) {
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer failServer.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hostDetailsKnowledge).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(failServer.URL),
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		res, ok := report.Resources[liquid.ResourceName("ram_"+flavorGroup)]
		if !ok {
			t.Fatal("expected resource to exist")
		}
		azReport := res.PerAZ[liquid.AvailabilityZone(az)]
		if azReport == nil {
			t.Fatal("expected non-nil AZ report on scheduler failure")
		}
		if azReport.Capacity != 0 {
			t.Errorf("expected capacity = 0 on failure, got %d", azReport.Capacity)
		}
	})

	t.Run("multiple AZs are reported independently", func(t *testing.T) {
		twoAZHostDetails := createTestHostDetailsKnowledge(t, map[string]string{
			"host-1": "az-a",
			"host-2": "az-b",
		})
		// Both calls always return 3 hosts regardless of AZ (pipeline-routing mock).
		server := newPipelineMockSchedulerServer(t, map[string][]string{
			"kvm-report-capacity": {"h1", "h2", "h3"},
			"kvm-general-purpose-load-balancing-all-filters-enabled": {"h1"},
		})
		defer server.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, twoAZHostDetails).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		res := report.Resources[liquid.ResourceName("ram_"+flavorGroup)]
		if len(res.PerAZ) != 2 {
			t.Errorf("expected 2 AZs, got %d", len(res.PerAZ))
		}
		if _, ok := res.PerAZ[liquid.AvailabilityZone("az-a")]; !ok {
			t.Error("expected az-a in report")
		}
		if _, ok := res.PerAZ[liquid.AvailabilityZone("az-b")]; !ok {
			t.Error("expected az-b in report")
		}
	})
}

// newPipelineMockSchedulerServer starts a test HTTP server that returns different
// host lists depending on the pipeline name in the request body.
func newPipelineMockSchedulerServer(t *testing.T, hostsByPipeline map[string][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req novaapi.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		hosts := hostsByPipeline[req.Pipeline]
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(novaapi.ExternalSchedulerResponse{Hosts: hosts}); err != nil {
			t.Errorf("mock scheduler: encode error: %v", err)
		}
	}))
}

// createTestFlavorGroupKnowledgeWithSmallest creates a Knowledge CRD where smallestFlavor
// is explicitly set so the capacity calculator uses the correct memory unit.
func createTestFlavorGroupKnowledgeWithSmallest(t *testing.T, groupName string, memMB, vcpus uint64) *v1alpha1.Knowledge {
	t.Helper()

	features := []map[string]interface{}{
		{
			"name": groupName,
			"flavors": []map[string]interface{}{
				{
					"name":     "test_flavor",
					"vcpus":    vcpus,
					"memoryMB": memMB,
					"diskGB":   50,
				},
			},
			"smallestFlavor": map[string]interface{}{
				"name":     "test_flavor",
				"vcpus":    vcpus,
				"memoryMB": memMB,
				"diskGB":   50,
			},
			"largestFlavor": map[string]interface{}{
				"name":     "test_flavor",
				"vcpus":    vcpus,
				"memoryMB": memMB,
				"diskGB":   50,
			},
		},
	}

	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:        raw,
		},
	}
}

// createTestHostDetailsKnowledge creates a Knowledge CRD with host→AZ mappings.
func createTestHostDetailsKnowledge(t *testing.T, hostToAZ map[string]string) *v1alpha1.Knowledge {
	t.Helper()

	features := make([]map[string]interface{}, 0, len(hostToAZ))
	for host, az := range hostToAZ {
		features = append(features, map[string]interface{}{
			"computeHost":      host,
			"availabilityZone": az,
		})
	}

	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{Name: "host-details"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "host_details"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []v1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: "True"}},
			Raw:        raw,
		},
	}
}

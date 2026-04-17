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

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func TestHandleReportCapacity(t *testing.T) {
	scheme := testScheme(t)

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
	scheme := testScheme(t)

	t.Run("CalculateCapacity returns error when no flavor groups knowledge exists", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		calculator := NewCapacityCalculator(fakeClient, DefaultConfig())
		_, err := calculator.CalculateCapacity(context.Background())
		if err == nil {
			t.Fatal("Expected error when flavor groups knowledge doesn't exist, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})

	t.Run("CalculateCapacity returns empty report when flavor groups knowledge exists but is empty", func(t *testing.T) {
		emptyKnowledge := createEmptyFlavorGroupKnowledge()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(emptyKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient, DefaultConfig())
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

	t.Run("CalculateCapacity returns perAZ entries for all AZs from hypervisors", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		hvs := createTestHypervisorsWithAZ(map[string]string{
			"host-1": "qa-de-1a",
			"host-2": "qa-de-1b",
		})
		server := newMockSchedulerServer(t, []string{})
		defer server.Close()
		cfg := DefaultConfig()
		cfg.SchedulerURL = server.URL
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hvs[0], hvs[1]).
			Build()

		calculator := NewCapacityCalculator(fakeClient, cfg)
		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(report.Resources) != 3 {
			t.Fatalf("Expected 3 resources (_ram, _cores, _instances), got %d", len(report.Resources))
		}

		// Verify all resources have entries for the AZs from hypervisors
		expectedAZs := []liquid.AvailabilityZone{"qa-de-1a", "qa-de-1b"}
		for _, resName := range []string{"hw_version_test-group_ram", "hw_version_test-group_cores", "hw_version_test-group_instances"} {
			res := report.Resources[liquid.ResourceName(resName)]
			if res == nil {
				t.Errorf("resource %s not found", resName)
				continue
			}
			for _, az := range expectedAZs {
				if _, ok := res.PerAZ[az]; !ok {
					t.Errorf("%s: missing entry for AZ %s", resName, az)
				}
			}
		}
	})

	t.Run("CalculateCapacity with no host details returns empty perAZ maps", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := NewCapacityCalculator(fakeClient, DefaultConfig())
		report, err := calculator.CalculateCapacity(context.Background())
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

	t.Run("CalculateCapacity produces perAZ entries matching hypervisor AZs", func(t *testing.T) {
		flavorGroupKnowledge := createTestFlavorGroupKnowledge(t, "test-group")
		hvs := createTestHypervisorsWithAZ(map[string]string{
			"host-a": "eu-de-1a",
			"host-b": "eu-de-1b",
		})
		server := newMockSchedulerServer(t, []string{})
		defer server.Close()
		cfg := DefaultConfig()
		cfg.SchedulerURL = server.URL
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hvs[0], hvs[1]).
			Build()

		calculator := NewCapacityCalculator(fakeClient, cfg)
		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		for resName, res := range report.Resources {
			if len(res.PerAZ) != 2 {
				t.Errorf("%s: expected 2 AZs, got %d", resName, len(res.PerAZ))
			}
			for _, az := range []liquid.AvailabilityZone{"eu-de-1a", "eu-de-1b"} {
				if _, ok := res.PerAZ[az]; !ok {
					t.Errorf("%s: missing entry for AZ %s", resName, az)
				}
			}
		}
	})
}

func TestCapacityCalculatorWithHypervisors(t *testing.T) {
	scheme := testScheme(t)

	const (
		flavorGroup = "test-group"
		az          = "az-a"
		flavorMemMB = uint64(32768) // 32 GiB
		flavorVCPUs = uint64(8)
	)

	flavorGroupKnowledge := createTestFlavorGroupKnowledgeWithSmallest(t, flavorGroup, flavorMemMB, flavorVCPUs)

	t.Run("computes capacity as multiples of smallest flavor", func(t *testing.T) {
		// Host has 256 GiB effective capacity. Smallest flavor = 32 GiB.
		// Total capacity = floor(256 / 32) = 8.
		server := newMockSchedulerServer(t, []string{"host-1"})
		defer server.Close()

		hvObj := createTestHypervisorWithAZ("host-1", az, "256Gi", "64Gi")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hvObj).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{"host-1": *hvObj}
		capacity, err := calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capacity != 8 {
			t.Errorf("expected capacity = 8, got %d", capacity)
		}
	})

	t.Run("sums multiples across multiple hosts", func(t *testing.T) {
		// Host-1: 256 GiB → total=8
		// Host-2: 128 GiB → total=4
		// Combined: total=12
		server := newMockSchedulerServer(t, []string{"host-1", "host-2"})
		defer server.Close()

		host1HV := createTestHypervisorWithAZ("host-1", az, "256Gi", "128Gi")
		host2HV := createTestHypervisorWithAZ("host-2", az, "128Gi", "0")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, host1HV, host2HV).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{"host-1": *host1HV, "host-2": *host2HV}
		capacity, err := calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capacity != 12 {
			t.Errorf("expected capacity = 12, got %d", capacity)
		}
	})

	t.Run("capacity is correct when nothing is allocated", func(t *testing.T) {
		server := newMockSchedulerServer(t, []string{"host-1"})
		defer server.Close()

		hvObj := createTestHypervisorWithAZ("host-1", az, "128Gi", "0")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hvObj).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{"host-1": *hvObj}
		capacity, err := calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capacity != 4 {
			t.Errorf("expected capacity = 4, got %d", capacity)
		}
	})

	t.Run("host not found in HV CRDs is skipped", func(t *testing.T) {
		// Scheduler returns a host with no matching HV CRD — should contribute 0 capacity.
		server := newMockSchedulerServer(t, []string{"host-unknown"})
		defer server.Close()

		hostDetails := createTestHypervisorsWithAZ(map[string]string{"host-unknown": az})

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hostDetails[0]).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{} // empty
		capacity, err := calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capacity != 0 {
			t.Errorf("expected capacity = 0, got %d", capacity)
		}
	})

	t.Run("scheduler failure returns error", func(t *testing.T) {
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer failServer.Close()

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(failServer.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{}
		_, err = calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err == nil {
			t.Fatal("expected error on scheduler failure, got nil")
		}
	})

	t.Run("multiple AZs are reported independently", func(t *testing.T) {
		// Scheduler always returns both hosts (mock doesn't filter by AZ).
		server := newMockSchedulerServer(t, []string{"host-1", "host-2"})
		defer server.Close()

		host1HV := createTestHypervisorWithAZ("host-1", "az-a", "128Gi", "32Gi")
		host2HV := createTestHypervisorWithAZ("host-2", "az-b", "64Gi", "0")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, host1HV, host2HV).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		report, err := calculator.CalculateCapacity(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		res := report.Resources[liquid.ResourceName(ResourceNameRAM(flavorGroup))]
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

	t.Run("partial memory is floored to full multiples", func(t *testing.T) {
		// Host has 100 GiB capacity. Smallest flavor = 32 GiB.
		// Total = floor(100 / 32) = 3 (not 3.125).
		server := newMockSchedulerServer(t, []string{"host-1"})
		defer server.Close()

		hvObj := createTestHypervisorWithAZ("host-1", az, "100Gi", "0")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(flavorGroupKnowledge, hvObj).
			Build()

		calculator := &CapacityCalculator{
			client:          fakeClient,
			schedulerClient: reservations.NewSchedulerClient(server.URL),
			totalPipeline:   "kvm-report-capacity",
		}

		knowledge := &reservations.FlavorGroupKnowledgeClient{Client: fakeClient}
		groups, err := knowledge.GetAllFlavorGroups(context.Background(), nil)
		if err != nil {
			t.Fatalf("failed to get flavor groups: %v", err)
		}

		hvByName := map[string]hv1.Hypervisor{"host-1": *hvObj}
		capacity, err := calculator.calculateInstanceCapacity(context.Background(), groups[flavorGroup], az, hvByName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capacity != 3 {
			t.Errorf("expected capacity = 3 (floored), got %d", capacity)
		}
	})
}

// newMockSchedulerServer returns a test HTTP server that always returns the given host list.
func newMockSchedulerServer(t *testing.T, hosts []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(novaapi.ExternalSchedulerResponse{Hosts: hosts}); err != nil {
			t.Errorf("mock scheduler: encode error: %v", err)
		}
	}))
}

// createTestHypervisor creates an HV CRD with the given effective capacity and allocation.
func createTestHypervisor(name, effectiveCapacity, allocation string) *hv1.Hypervisor {
	hv := &hv1.Hypervisor{
		ObjectMeta: v1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(effectiveCapacity),
			},
		},
	}
	if allocation != "0" && allocation != "" {
		hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: resource.MustParse(allocation),
		}
	}
	return hv
}

// createTestHypervisorWithAZ creates an HV CRD with a topology.kubernetes.io/zone label.
func createTestHypervisorWithAZ(name, az, effectiveCapacity, allocation string) *hv1.Hypervisor {
	hv := createTestHypervisor(name, effectiveCapacity, allocation)
	hv.Labels = map[string]string{"topology.kubernetes.io/zone": az}
	return hv
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
			"smallestFlavor": map[string]interface{}{
				"name":     "test_c8_m32",
				"vcpus":    8,
				"memoryMB": 32768,
				"diskGB":   50,
			},
			"ramCoreRatio": 4096,
		},
	}

	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatal(err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: v1.ObjectMeta{
			Name: "flavor-groups",
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

// createTestHypervisorsWithAZ creates HV CRDs with topology.kubernetes.io/zone labels
// from a host→AZ map. Hypervisors have no capacity data (used only for AZ discovery).
func createTestHypervisorsWithAZ(hostToAZ map[string]string) []*hv1.Hypervisor {
	hvs := make([]*hv1.Hypervisor, 0, len(hostToAZ))
	for host, az := range hostToAZ {
		hv := &hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name:   host,
				Labels: map[string]string{"topology.kubernetes.io/zone": az},
			},
		}
		hvs = append(hvs, hv)
	}
	return hvs
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Test Helpers
// ============================================================================

func buildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

func newHypervisor(name, cpuCap, cpuAlloc, memCap, memAlloc string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpuCap),
				"memory": resource.MustParse(memCap),
			},
			Allocation: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpuAlloc),
				"memory": resource.MustParse(memAlloc),
			},
		},
	}
}

func newCommittedReservation(name, targetHost, observedHost, projectID, flavorName, flavorGroup, cpu, memory string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpu),
				"memory": resource.MustParse(memory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceName:  flavorName,
				ResourceGroup: flavorGroup,
				Allocations:   nil,
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
			Host: observedHost,
		},
	}
}

func newFailoverReservation(name, targetHost, resourceGroup, cpu, memory string, allocations map[string]string) *v1alpha1.Reservation { //nolint:unparam // resourceGroup varies in real usage
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpu),
				"memory": resource.MustParse(memory),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: resourceGroup,
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
			Host: targetHost,
		},
	}
	if allocations != nil {
		res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
			Allocations: allocations,
		}
	}
	return res
}

// parseMemoryToMB converts a memory string (e.g., "8Gi", "4096Mi") to megabytes.
func parseMemoryToMB(memory string) uint64 {
	q := resource.MustParse(memory)
	bytes := q.Value()
	return uint64(bytes / (1024 * 1024)) //nolint:gosec // test code
}

func newNovaRequest(instanceUUID, projectID, flavorName, flavorGroup string, vcpus int, memory string, evacuation bool, hosts []string, pipeline string) novaapi.ExternalSchedulerRequest { //nolint:unparam // pipeline varies in real usage
	hostList := make([]novaapi.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = novaapi.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
		"hw_version":                   flavorGroup,
	}

	var schedulerHints map[string]any
	if evacuation {
		schedulerHints = map[string]any{
			"_nova_check_type": []any{"evacuate"},
		}
	}

	memoryMB := parseMemoryToMB(memory)

	spec := novaapi.NovaSpec{
		ProjectID:      projectID,
		InstanceUUID:   instanceUUID,
		NumInstances:   1,
		SchedulerHints: schedulerHints,
		Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
			Data: novaapi.NovaFlavor{
				Name:       flavorName,
				VCPUs:      uint64(vcpus), //nolint:gosec // test code
				MemoryMB:   memoryMB,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	weights := make(map[string]float64)
	for _, h := range hosts {
		weights[h] = 1.0
	}

	req := novaapi.ExternalSchedulerRequest{
		Spec:    novaapi.NovaObject[novaapi.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}

	if pipeline != "" {
		req.Pipeline = pipeline
	}

	return req
}

// ============================================================================
// Integration Test Infrastructure
// ============================================================================

// metricsRegistered ensures we only register metrics once per test run
var metricsRegistered sync.Once
var sharedMonitor lib.FilterWeigherPipelineMonitor

func getSharedMonitor() lib.FilterWeigherPipelineMonitor {
	metricsRegistered.Do(func() {
		sharedMonitor = lib.NewPipelineMonitor()
	})
	return sharedMonitor
}

// IntegrationTestServer wraps the HTTP API with a real scheduling pipeline
// backed by a fake k8s client. This allows sending real HTTP requests
// to test the full scheduling flow.
type IntegrationTestServer struct {
	Server     *httptest.Server
	K8sClient  client.Client
	Controller *FilterWeigherPipelineController
}

// PipelineConfig defines the filters and weighers for a test pipeline
type PipelineConfig struct {
	Name     string
	Filters  []v1alpha1.FilterSpec
	Weighers []v1alpha1.WeigherSpec
}

// DefaultPipelineConfig returns the default pipeline configuration with
// filter_has_enough_capacity and kvm_failover_evacuation
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		Name: "kvm-general-purpose-load-balancing-all-filters-enabled",
		Filters: []v1alpha1.FilterSpec{
			{Name: "filter_has_enough_capacity"},
		},
		Weighers: []v1alpha1.WeigherSpec{
			{Name: "kvm_failover_evacuation"},
		},
	}
}

// NewIntegrationTestServer creates a test server with:
// - Fake k8s client for CRD operations (reservations, hypervisors, etc.)
// - Real HTTP server for NovaExternalScheduler endpoint
// - Real scheduling pipeline with filters and weighers
func NewIntegrationTestServer(t *testing.T, pipelineConfig PipelineConfig, objects ...client.Object) *IntegrationTestServer {
	t.Helper()

	scheme := buildTestScheme(t)

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	// Create the pipeline controller with shared monitor to avoid duplicate registration
	controller := &FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor: getSharedMonitor(),
	}

	// Create and register the test pipeline
	testPipeline := v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: pipelineConfig.Name,
		},
		Spec: v1alpha1.PipelineSpec{
			Type:     v1alpha1.PipelineTypeFilterWeigher,
			Filters:  pipelineConfig.Filters,
			Weighers: pipelineConfig.Weighers,
		},
	}

	// Initialize the pipeline
	ctx := context.Background()
	result := lib.InitNewFilterWeigherPipeline(
		ctx, k8sClient, testPipeline.Name,
		filters.Index, testPipeline.Spec.Filters,
		weighers.Index, testPipeline.Spec.Weighers,
		controller.Monitor,
	)
	if len(result.FilterErrors) > 0 || len(result.WeigherErrors) > 0 {
		t.Fatalf("Failed to init pipeline: filters=%v, weighers=%v", result.FilterErrors, result.WeigherErrors)
	}
	controller.Pipelines[testPipeline.Name] = result.Pipeline
	controller.PipelineConfigs[testPipeline.Name] = testPipeline

	// Create the HTTP API with the controller as delegate - skip metrics registration
	api := &httpAPI{
		config:   HTTPAPIConfig{},
		monitor:  lib.NewSchedulerMonitor(), // Create new monitor but don't register
		delegate: controller,
	}

	// Create test server
	mux := http.NewServeMux()
	mux.HandleFunc("/scheduler/nova/external", api.NovaExternalScheduler)
	server := httptest.NewServer(mux)

	return &IntegrationTestServer{
		Server:     server,
		K8sClient:  k8sClient,
		Controller: controller,
	}
}

func (s *IntegrationTestServer) Close() {
	s.Server.Close()
}

func (s *IntegrationTestServer) SendPlacementRequest(t *testing.T, req novaapi.ExternalSchedulerRequest) novaapi.ExternalSchedulerResponse {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	resp, err := http.Post(s.Server.URL+"/scheduler/nova/external", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var response novaapi.ExternalSchedulerResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	return response
}

// CreateReservation adds a reservation to the fake k8s client
func (s *IntegrationTestServer) CreateReservation(t *testing.T, res *v1alpha1.Reservation) {
	t.Helper()
	if err := s.K8sClient.Create(context.Background(), res); err != nil {
		t.Fatalf("Failed to create reservation: %v", err)
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

func TestIntegration_SchedulingWithReservations(t *testing.T) {
	tests := []struct {
		name         string
		hypervisors  []*hv1.Hypervisor
		reservations []*v1alpha1.Reservation
		request      novaapi.ExternalSchedulerRequest

		// Pipeline configuration (optional - uses default if nil)
		filters  []v1alpha1.FilterSpec
		weighers []v1alpha1.WeigherSpec

		// Expected results
		expectedHosts         []string // Hosts that should be in the response (in order if expectedHostsOrdered is true)
		expectedHostsOrdered  bool     // If true, expectedHosts must match response order exactly
		filteredHosts         []string // Hosts that should NOT be in the response
		minExpectedHostsCount int      // Minimum number of hosts expected in response
	}{
		{
			name: "Initial placement ignores failover reservations - host with reservation filtered out",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"),
				// host3 has limited capacity - just enough for the failover reservation
				newHypervisor("host3", "8", "4", "16Gi", "8Gi"),
			},
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-vm-existing", "host3", "m1.large", "4", "8Gi", map[string]string{"vm-existing": "host1"}),
			},
			request:               newNovaRequest("new-vm-uuid", "project-B", "m1.medium", "gp-1", 2, "4Gi", false, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			filteredHosts:         []string{"host3"},
			minExpectedHostsCount: 2,
		},
		{
			name: "Evacuation prefers failover reservation host",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"),
				newHypervisor("host3", "16", "8", "32Gi", "16Gi"),
			},
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-vm-123", "host3", "m1.large", "4", "8Gi", map[string]string{"vm-123": "host1"}),
			},
			request:               newNovaRequest("vm-123", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host2", "host3"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			expectedHosts:         []string{"host3", "host2"}, // Failover host should be first
			expectedHostsOrdered:  true,
			minExpectedHostsCount: 2,
		},
		{
			name: "Evacuation with multiple failover reservations - VM has reservations on multiple hosts",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "12", "32Gi", "16Gi"),
				newHypervisor("host2", "16", "12", "32Gi", "16Gi"),
				newHypervisor("host3", "16", "12", "32Gi", "16Gi"),
			},
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-vm-456-on-host1", "host1", "m1.large", "4", "8Gi", map[string]string{"vm-456": "host-original"}),
				newFailoverReservation("failover-vm-456-on-host3", "host3", "m1.large", "4", "8Gi", map[string]string{"vm-456": "host-original"}),
			},
			request:               newNovaRequest("vm-456", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			expectedHosts:         []string{"host1", "host2", "host3"},
			minExpectedHostsCount: 3,
			// Both host1 and host3 have failover reservations, so they should be preferred over host2
		},
		{
			name: "Evacuation with multiple failover reservations - VM has one reservation",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "12", "32Gi", "16Gi"),
				newHypervisor("host2", "16", "12", "32Gi", "16Gi"),
				newHypervisor("host3", "16", "12", "32Gi", "16Gi"),
			},
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-vm-456-on-host1", "host1", "m1.large", "4", "8Gi", map[string]string{"some-other-vm": "host-original"}),
				newFailoverReservation("failover-vm-456-on-host3", "host3", "m1.large", "4", "8Gi", map[string]string{"vm-456": "host-original"}),
			},
			request:               newNovaRequest("vm-456", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			expectedHosts:         []string{"host3", "host2"},
			expectedHostsOrdered:  true,
			minExpectedHostsCount: 2,
			// Both host1 and host3 have failover reservations, so they should be preferred over host2
		},
		{
			name: "Initial placement with committed resource reservation - matching project/flavor unlocks capacity",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "12", "32Gi", "24Gi"), // 4 CPU free
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"),  // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("committed-res-host1", "host1", "host1", "project-A", "m1.large", "gp-1", "4", "8Gi"),
			},
			request:               newNovaRequest("new-vm should work", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			expectedHosts:         []string{"host1", "host2"}, // host1 unlocked because project/flavor match
			minExpectedHostsCount: 2,
		},
		{
			name: "Initial placement with committed resource reservation - non-matching project blocks capacity",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "8", "4", "16Gi", "8Gi"),   // 4 CPU free, but reserved
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("committed-res-host1", "host1", "host1", "project-A", "m1.large", "gp-1", "4", "8Gi"),
			},
			request:               newNovaRequest("new-vm", "project-B", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			expectedHosts:         []string{"host2"},
			filteredHosts:         []string{"host1"}, // host1 blocked because project doesn't match
			minExpectedHostsCount: 1,
		},
		{
			name: "Filter only - no weighers",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
				newHypervisor("host2", "8", "8", "16Gi", "16Gi"), // No capacity
				newHypervisor("host3", "16", "4", "32Gi", "8Gi"),
			},
			reservations:          []*v1alpha1.Reservation{},
			request:               newNovaRequest("new-vm", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing-all-filters-enabled"),
			filters:               []v1alpha1.FilterSpec{{Name: "filter_has_enough_capacity"}},
			weighers:              []v1alpha1.WeigherSpec{}, // No weighers
			filteredHosts:         []string{"host2"},
			minExpectedHostsCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build objects
			objects := make([]client.Object, 0, len(tt.hypervisors)+len(tt.reservations))
			for _, hv := range tt.hypervisors {
				objects = append(objects, hv)
			}
			for _, res := range tt.reservations {
				objects = append(objects, res)
			}

			// Determine pipeline config
			pipelineConfig := DefaultPipelineConfig()
			if tt.filters != nil {
				pipelineConfig.Filters = tt.filters
			}
			if tt.weighers != nil {
				pipelineConfig.Weighers = tt.weighers
			}

			server := NewIntegrationTestServer(t, pipelineConfig, objects...)
			defer server.Close()

			response := server.SendPlacementRequest(t, tt.request)

			t.Logf("Response hosts: %v", response.Hosts)

			// Verify minimum host count
			if len(response.Hosts) < tt.minExpectedHostsCount {
				t.Errorf("Expected at least %d hosts, got %d: %v", tt.minExpectedHostsCount, len(response.Hosts), response.Hosts)
			}

			// Verify expected hosts
			if tt.expectedHostsOrdered {
				// Verify exact order
				if len(response.Hosts) != len(tt.expectedHosts) {
					t.Errorf("Expected %d hosts in order %v, got %d hosts: %v", len(tt.expectedHosts), tt.expectedHosts, len(response.Hosts), response.Hosts)
				} else {
					for i, expectedHost := range tt.expectedHosts {
						if response.Hosts[i] != expectedHost {
							t.Errorf("Expected host at position %d to be %s, got %s. Full response: %v", i, expectedHost, response.Hosts[i], response.Hosts)
						}
					}
				}
			} else if len(tt.expectedHosts) > 0 {
				// Verify hosts are present (any order)
				for _, expectedHost := range tt.expectedHosts {
					if !slices.Contains(response.Hosts, expectedHost) {
						t.Errorf("Expected host %s to be in response, but it was not. Response: %v", expectedHost, response.Hosts)
					}
				}
			}

			// Verify filtered hosts are NOT present
			for _, filteredHost := range tt.filteredHosts {
				if slices.Contains(response.Hosts, filteredHost) {
					t.Errorf("Expected host %s to be filtered out, but it was in response: %v", filteredHost, response.Hosts)
				}
			}
		})
	}
}

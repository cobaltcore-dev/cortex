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
	th "github.com/cobaltcore-dev/cortex/internal/scheduling/nova/testhelpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
		Name: "nova-external-scheduler-kvm-all-filters-enabled",
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

	scheme := th.BuildTestScheme(t)

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

// IntegrationTestCase defines a parameterized integration test case
type IntegrationTestCase struct {
	Name         string
	Hypervisors  []th.HypervisorArgs
	Reservations []th.ReservationArgs
	Request      th.NovaRequestArgs

	// Pipeline configuration (optional - uses default if nil)
	Filters  []v1alpha1.FilterSpec
	Weighers []v1alpha1.WeigherSpec

	// Expected results
	ExpectedHosts         []string // Hosts that should be in the response (in order if ExpectedHostsOrdered is true)
	ExpectedHostsOrdered  bool     // If true, ExpectedHosts must match response order exactly
	FilteredHosts         []string // Hosts that should NOT be in the response
	MinExpectedHostsCount int      // Minimum number of hosts expected in response
}

func TestIntegration_SchedulingWithReservations(t *testing.T) {
	tests := []IntegrationTestCase{
		{
			Name: "Initial placement ignores failover reservations - host with reservation filtered out",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host2", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
				// host3 has limited capacity - just enough for the failover reservation
				{Name: "host3", CPUCap: "8", CPUAlloc: "4", MemCap: "16Gi", MemAlloc: "8Gi"},
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "failover-vm-existing", TargetHost: "host3", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"vm-existing": "host1"},
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "new-vm-uuid", ProjectID: "project-B", FlavorName: "m1.medium",
				VCPUs: 2, Memory: "4Gi", Evacuation: false,
				Hosts: []string{"host1", "host2", "host3"},
			},
			FilteredHosts:         []string{"host3"},
			MinExpectedHostsCount: 2,
		},
		{
			Name: "Evacuation prefers failover reservation host",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host2", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host3", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "failover-vm-123", TargetHost: "host3", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"vm-123": "host1"},
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "vm-123", ProjectID: "project-A", FlavorName: "m1.large",
				VCPUs: 4, Memory: "8Gi", Evacuation: true,
				Hosts: []string{"host2", "host3"},
			},
			ExpectedHosts:         []string{"host3", "host2"}, // Failover host should be first
			ExpectedHostsOrdered:  true,
			MinExpectedHostsCount: 2,
		},
		{
			Name: "Evacuation with multiple failover reservations - VM has reservations on multiple hosts",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host2", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host3", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "failover-vm-456-on-host1", TargetHost: "host1", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"vm-456": "host-original"},
				},
				{
					Name: "failover-vm-456-on-host3", TargetHost: "host3", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"vm-456": "host-original"},
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "vm-456", ProjectID: "project-A", FlavorName: "m1.large",
				VCPUs: 4, Memory: "8Gi", Evacuation: true,
				Hosts: []string{"host1", "host2", "host3"},
			},
			ExpectedHosts:         []string{"host1", "host2", "host3"},
			MinExpectedHostsCount: 3,
			// Both host1 and host3 have failover reservations, so they should be preferred over host2
		},
		{
			Name: "Evacuation with multiple failover reservations - VM has one reservation",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host2", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host3", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "16Gi"},
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "failover-vm-456-on-host1", TargetHost: "host1", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"some-other-vm": "host-original"},
				},
				{
					Name: "failover-vm-456-on-host3", TargetHost: "host3", CPU: "4", Memory: "8Gi",
					ResourceGroup: "m1.large", Allocations: map[string]string{"vm-456": "host-original"},
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "vm-456", ProjectID: "project-A", FlavorName: "m1.large",
				VCPUs: 4, Memory: "8Gi", Evacuation: true,
				Hosts: []string{"host1", "host2", "host3"},
			},
			ExpectedHosts:         []string{"host3", "host2"},
			ExpectedHostsOrdered:  true,
			MinExpectedHostsCount: 2,
			// Both host1 and host3 have failover reservations, so they should be preferred over host2
		},
		{
			Name: "Initial placement with committed resource reservation - matching project/flavor unlocks capacity",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "16", CPUAlloc: "12", MemCap: "32Gi", MemAlloc: "24Gi"}, // 4 CPU free
				{Name: "host2", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},  // 8 CPU free
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "committed-res-host1", TargetHost: "host1", CPU: "4", Memory: "8Gi",
					ProjectID: "project-A", ResourceName: "m1.large",
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "new-vm", ProjectID: "project-A", FlavorName: "m1.large",
				VCPUs: 4, Memory: "8Gi", Evacuation: false,
				Hosts: []string{"host1", "host2"},
			},
			ExpectedHosts:         []string{"host1", "host2"}, // host1 unlocked because project/flavor match
			MinExpectedHostsCount: 2,
		},
		{
			Name: "Initial placement with committed resource reservation - non-matching project blocks capacity",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "8", CPUAlloc: "4", MemCap: "16Gi", MemAlloc: "8Gi"},   // 4 CPU free, but reserved
				{Name: "host2", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"}, // 8 CPU free
			},
			Reservations: []th.ReservationArgs{
				{
					Name: "committed-res-host1", TargetHost: "host1", CPU: "4", Memory: "8Gi",
					ProjectID: "project-A", ResourceName: "m1.large",
				},
			},
			Request: th.NovaRequestArgs{
				InstanceUUID: "new-vm", ProjectID: "project-B", FlavorName: "m1.large", // Different project!
				VCPUs: 4, Memory: "8Gi", Evacuation: false,
				Hosts: []string{"host1", "host2"},
			},
			ExpectedHosts:         []string{"host2"},
			FilteredHosts:         []string{"host1"}, // host1 blocked because project doesn't match
			MinExpectedHostsCount: 1,
		},
		{
			Name: "Filter only - no weighers",
			Hypervisors: []th.HypervisorArgs{
				{Name: "host1", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
				{Name: "host2", CPUCap: "8", CPUAlloc: "8", MemCap: "16Gi", MemAlloc: "16Gi"}, // No capacity
				{Name: "host3", CPUCap: "16", CPUAlloc: "4", MemCap: "32Gi", MemAlloc: "8Gi"},
			},
			Reservations: []th.ReservationArgs{},
			Request: th.NovaRequestArgs{
				InstanceUUID: "new-vm", ProjectID: "project-A", FlavorName: "m1.large",
				VCPUs: 4, Memory: "8Gi", Evacuation: false,
				Hosts: []string{"host1", "host2", "host3"},
			},
			Filters:               []v1alpha1.FilterSpec{{Name: "filter_has_enough_capacity"}},
			Weighers:              []v1alpha1.WeigherSpec{}, // No weighers
			FilteredHosts:         []string{"host2"},
			MinExpectedHostsCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			// Build hypervisors
			objects := make([]client.Object, 0, len(tt.Hypervisors)+len(tt.Reservations))
			for _, hvOpts := range tt.Hypervisors {
				objects = append(objects, th.NewHypervisor(hvOpts))
			}

			// Build reservations
			for _, resOpts := range tt.Reservations {
				if resOpts.Allocations != nil {
					// Failover reservation
					objects = append(objects, th.NewFailoverReservation(resOpts))
				} else {
					// Committed resource reservation
					objects = append(objects, th.NewCommittedReservation(resOpts))
				}
			}

			// Determine pipeline config
			pipelineConfig := DefaultPipelineConfig()
			if tt.Filters != nil {
				pipelineConfig.Filters = tt.Filters
			}
			if tt.Weighers != nil {
				pipelineConfig.Weighers = tt.Weighers
			}

			// Set pipeline name in request
			tt.Request.Pipeline = pipelineConfig.Name

			server := NewIntegrationTestServer(t, pipelineConfig, objects...)
			defer server.Close()

			request := th.NewNovaRequest(tt.Request)
			response := server.SendPlacementRequest(t, request)

			t.Logf("Response hosts: %v", response.Hosts)

			// Verify minimum host count
			if len(response.Hosts) < tt.MinExpectedHostsCount {
				t.Errorf("Expected at least %d hosts, got %d: %v", tt.MinExpectedHostsCount, len(response.Hosts), response.Hosts)
			}

			// Verify expected hosts
			if tt.ExpectedHostsOrdered {
				// Verify exact order
				if len(response.Hosts) != len(tt.ExpectedHosts) {
					t.Errorf("Expected %d hosts in order %v, got %d hosts: %v", len(tt.ExpectedHosts), tt.ExpectedHosts, len(response.Hosts), response.Hosts)
				} else {
					for i, expectedHost := range tt.ExpectedHosts {
						if response.Hosts[i] != expectedHost {
							t.Errorf("Expected host at position %d to be %s, got %s. Full response: %v", i, expectedHost, response.Hosts[i], response.Hosts)
						}
					}
				}
			} else if len(tt.ExpectedHosts) > 0 {
				// Verify hosts are present (any order)
				for _, expectedHost := range tt.ExpectedHosts {
					if !slices.Contains(response.Hosts, expectedHost) {
						t.Errorf("Expected host %s to be in response, but it was not. Response: %v", expectedHost, response.Hosts)
					}
				}
			}

			// Verify filtered hosts are NOT present
			for _, filteredHost := range tt.FilteredHosts {
				if slices.Contains(response.Hosts, filteredHost) {
					t.Errorf("Expected host %s to be filtered out, but it was in response: %v", filteredHost, response.Hosts)
				}
			}
		})
	}
}

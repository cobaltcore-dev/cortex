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
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/crs"
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
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuCap),
				hv1.ResourceMemory: resource.MustParse(memCap),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuAlloc),
				hv1.ResourceMemory: resource.MustParse(memAlloc),
			},
		},
	}
}

// newHypervisorWithTraits is like newHypervisor but additionally sets the
// hypervisor's status traits (e.g. CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE).
func newHypervisorWithTraits(name, cpuCap, cpuAlloc, memCap, memAlloc string, traits []string) *hv1.Hypervisor { //nolint:unparam // capacity args vary in real usage
	hv := newHypervisor(name, cpuCap, cpuAlloc, memCap, memAlloc)
	hv.Status.Traits = traits
	return hv
}

func newCommittedReservation(name, targetHost, observedHost, projectID, flavorName, flavorGroup, cpu, memory string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpu),
				hv1.ResourceMemory: resource.MustParse(memory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceName:  flavorName,
				ResourceGroup: flavorGroup,
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
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpu),
				hv1.ResourceMemory: resource.MustParse(memory),
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
		"quota:hw_version":             flavorGroup,
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

// newDomainNovaRequest builds a minimal Nova request carrying a domain_name
// scheduler hint (and optionally a _nova_check_type hint to drive the request
// intent), used to exercise the domain-based filters. All hosts are offered with
// equal weight.
func newDomainNovaRequest(instanceUUID, domainName, checkType string, hosts []string) novaapi.ExternalSchedulerRequest {
	hostList := make([]novaapi.ExternalSchedulerHost, len(hosts))
	weights := make(map[string]float64, len(hosts))
	for i, h := range hosts {
		hostList[i] = novaapi.ExternalSchedulerHost{ComputeHost: h}
		weights[h] = 1.0
	}

	schedulerHints := map[string]any{}
	if domainName != "" {
		schedulerHints["domain_name"] = domainName
	}
	if checkType != "" {
		schedulerHints["_nova_check_type"] = []any{checkType}
	}

	spec := novaapi.NovaSpec{
		ProjectID:      "project-A",
		InstanceUUID:   instanceUUID,
		NumInstances:   1,
		SchedulerHints: schedulerHints,
		Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
			Data: novaapi.NovaFlavor{
				Name:     "m1.small",
				VCPUs:    1,
				MemoryMB: 512,
				ExtraSpecs: map[string]string{
					"capabilities:hypervisor_type": "qemu",
				},
			},
		},
	}

	return novaapi.ExternalSchedulerRequest{
		Spec:    novaapi.NovaObject[novaapi.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
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
		Name: "kvm-general-purpose-load-balancing",
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
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[compute.PlacementRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[compute.PlacementRequest]),
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

	// Create the HTTP API backed by a real compute.Placement over the controller.
	// KVM requests trigger candidate gathering; use a passthrough gatherer
	// (nil gatheredHosts) so the test's preselected hosts are preserved.
	scheduler := compute.NewPlacement(controller, &mockCandidateGatherer{})
	api := &httpAPI{
		config:     HTTPAPIConfig{},
		monitor:    lib.NewSchedulerMonitor(), // Create new monitor but don't register
		scheduler:  scheduler,
		vmHandler:  &VirtualMachineHandler{Client: k8sClient},
		crRecorder: crs.Recorder{Client: k8sClient},
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
			request:               newNovaRequest("new-vm-uuid", "project-B", "m1.medium", "gp-1", 2, "4Gi", false, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("vm-123", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host2", "host3"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("vm-456", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("vm-456", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("new-vm should work", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("new-vm", "project-B", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}, "kvm-general-purpose-load-balancing"),
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
			request:               newNovaRequest("new-vm", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3"}, "kvm-general-purpose-load-balancing"),
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

// TestIntegration_ExternalCustomerAndDomainPinning exercises the interplay of
// filter_external_customer and filter_domain_pinning through the full scheduler
// HTTP flow (hint parsing, request-type parsing, param decoding and filter
// order). filter_has_enough_capacity is included to mirror production; every
// host carries ample free capacity so it never removes a host.
func TestIntegration_ExternalCustomerAndDomainPinning(t *testing.T) {
	const exclusive = "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"
	// Topology (all hosts have plenty of free capacity):
	//   excl-shared-1/2: exclusive, shared external-customer pool
	//   excl-pinned-a:   exclusive, pinned to iaas-pinned-a
	//   excl-pinned-b:   exclusive, pinned to iaas-pinned-b
	//   regular:         no exclusive trait
	hypervisors := []*hv1.Hypervisor{
		newHypervisorWithTraits("excl-shared-1", "64", "0", "128Gi", "0", []string{exclusive}),
		newHypervisorWithTraits("excl-shared-2", "64", "0", "128Gi", "0", []string{exclusive}),
		newHypervisorWithTraits("excl-pinned-a", "64", "0", "128Gi", "0", []string{exclusive}),
		newHypervisorWithTraits("excl-pinned-b", "64", "0", "128Gi", "0", []string{exclusive}),
		newHypervisor("regular", "64", "0", "128Gi", "0"),
	}
	allHosts := []string{"excl-shared-1", "excl-shared-2", "excl-pinned-a", "excl-pinned-b", "regular"}

	allowListPipeline := func(allowed []string, pinning map[string][]string) PipelineConfig {
		return PipelineConfig{
			Name: "kvm-general-purpose-load-balancing",
			Filters: []v1alpha1.FilterSpec{
				{Name: "filter_external_customer", Params: v1alpha1.Parameters{
					{Key: "domainNamePrefixes", StringListValue: new([]string{"iaas-"})},
					{Key: "allowedDomainNames", StringListValue: new(allowed)},
				}},
				{Name: "filter_domain_pinning", Params: v1alpha1.Parameters{
					{Key: "domainHypervisors", StringListMapValue: new(pinning)},
				}},
				{Name: "filter_has_enough_capacity"},
			},
		}
	}

	defaultAllowed := []string{"iaas-pinned-a", "iaas-pinned-b", "iaas-shared-*"}
	defaultPinning := map[string][]string{
		"iaas-pinned-a": {"excl-pinned-a"},
		"iaas-pinned-b": {"excl-pinned-b"},
	}

	tests := []struct {
		name          string
		allowed       []string
		pinning       map[string][]string
		request       novaapi.ExternalSchedulerRequest
		expectedHosts []string // exact set of hosts expected back
	}{
		{
			name:          "allow-listed unpinned domain uses shared exclusive pool",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm1", "iaas-shared-cust", "", allHosts),
			expectedHosts: []string{"excl-shared-1", "excl-shared-2"},
		},
		{
			name:          "pinned domain a lands only on its dedicated hypervisor",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm2", "iaas-pinned-a", "", allHosts),
			expectedHosts: []string{"excl-pinned-a"},
		},
		{
			name:          "pinned domain b lands only on its dedicated hypervisor",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm3", "iaas-pinned-b", "", allHosts),
			expectedHosts: []string{"excl-pinned-b"},
		},
		{
			name:          "iaas domain absent from allow-list gets no hosts",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm4", "iaas-rogue", "", allHosts),
			expectedHosts: []string{},
		},
		{
			name:          "regular tenant kept off all exclusive hosts",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm5", "internal-corp", "", allHosts),
			expectedHosts: []string{"regular"},
		},
		{
			name:          "failover reservation for pinned domain confined to its hypervisor",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm6", "iaas-pinned-a", "reserve_for_failover", allHosts),
			expectedHosts: []string{"excl-pinned-a"},
		},
		{
			name:          "failover reuse for unpinned allow-listed domain uses shared pool",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm7", "iaas-shared-cust", "reuse_failover_reservation", allHosts),
			expectedHosts: []string{"excl-shared-1", "excl-shared-2"},
		},
		{
			name:          "capacity probe (no domain) skips domain filters and sees all hosts",
			allowed:       defaultAllowed,
			pinning:       defaultPinning,
			request:       newDomainNovaRequest("vm8", "", "capacity_probe", allHosts),
			expectedHosts: []string{"excl-shared-1", "excl-shared-2", "excl-pinned-a", "excl-pinned-b", "regular"},
		},
		{
			name:          "fail-closed with pinning disabled blocks iaas domains",
			allowed:       nil,
			pinning:       map[string][]string{},
			request:       newDomainNovaRequest("vm9", "iaas-shared-cust", "", allHosts),
			expectedHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(hypervisors))
			for _, hv := range hypervisors {
				objects = append(objects, hv)
			}
			server := NewIntegrationTestServer(t, allowListPipeline(tt.allowed, tt.pinning), objects...)
			defer server.Close()

			response := server.SendPlacementRequest(t, tt.request)

			if len(response.Hosts) != len(tt.expectedHosts) {
				t.Fatalf("expected hosts %v, got %v", tt.expectedHosts, response.Hosts)
			}
			for _, h := range tt.expectedHosts {
				if !slices.Contains(response.Hosts, h) {
					t.Errorf("expected host %s in response, got %v", h, response.Hosts)
				}
			}
		})
	}
}

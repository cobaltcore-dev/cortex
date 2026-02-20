// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// mockCandidateGatherer implements CandidateGatherer for testing
type mockCandidateGatherer struct {
	called        bool
	err           error
	gatheredHosts []api.ExternalSchedulerHost
}

func (m *mockCandidateGatherer) MutateWithAllCandidates(ctx context.Context, request *api.ExternalSchedulerRequest) error {
	m.called = true
	if m.err != nil {
		return m.err
	}
	if m.gatheredHosts != nil {
		request.Hosts = m.gatheredHosts
		request.Weights = make(map[string]float64)
		for _, host := range m.gatheredHosts {
			request.Weights[host.ComputeHost] = 0.0
		}
	}
	return nil
}

func TestFilterWeigherPipelineController_InitPipeline(t *testing.T) {
	controller := &FilterWeigherPipelineController{
		Monitor: lib.FilterWeigherPipelineMonitor{},
	}

	tests := []struct {
		name                   string
		filters                []v1alpha1.FilterSpec
		weighers               []v1alpha1.WeigherSpec
		expectNonCriticalError bool
		expectCriticalError    bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "supported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "unsupported-plugin",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true,
		},
		{
			name: "step with scoping options",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
					Params: runtime.RawExtension{
						Raw: []byte(`{"scope":{"host_capabilities":{"any_of_trait_infixes":["TEST_TRAIT"]}}}`),
					},
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "step with invalid scoping options",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
					Params: runtime.RawExtension{
						Raw: []byte(`invalid json`),
					},
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Filters:  tt.filters,
					Weighers: tt.weighers,
				},
			})

			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("Expected critical error but got none")
			}
			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("Unexpected critical errors: %v", initResult.FilterErrors)
			}
			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("Expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("Unexpected non-critical errors: %v", initResult.WeigherErrors)
			}
		})
	}
}

func TestFilterWeigherPipelineController_ProcessRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	novaRequest := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Name:      "RequestSpec",
			Namespace: "nova_object",
			Version:   "1.19",
			Data: api.NovaSpec{
				ProjectID:    "test-project",
				UserID:       "test-user",
				InstanceUUID: "test-instance-uuid",
				NumInstances: 1,
			},
		},
		Context: api.NovaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: func() *string { s := "global-req-123"; return &s }(),
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "compute-1", HypervisorHostname: "hv-1"},
			{ComputeHost: "compute-2", HypervisorHostname: "hv-2"},
		},
		Weights:  map[string]float64{"compute-1": 1.0, "compute-2": 0.5},
		Pipeline: "test-pipeline",
	}

	tests := []struct {
		name                 string
		request              api.ExternalSchedulerRequest
		pipeline             *v1alpha1.Pipeline
		pipelineConf         *v1alpha1.Pipeline
		setupPipelineConfigs bool
		expectError          bool
		expectResult         bool
		expectUpdatedStatus  bool
		errorContains        string
	}{
		{
			name:    "successful processing with decision creation enabled",
			request: novaRequest,
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			expectError:          false,
			expectResult:         true,
			expectUpdatedStatus:  true,
		},
		{
			name:                 "pipeline not configured",
			request:              novaRequest,
			pipeline:             nil,
			pipelineConf:         nil,
			setupPipelineConfigs: false,
			expectError:          true,
			expectResult:         false,
			expectUpdatedStatus:  false,
			errorContains:        "pipeline test-pipeline not found or not ready",
		},
		{
			name:     "pipeline not found in runtime map",
			request:  novaRequest,
			pipeline: nil,
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "missing-runtime-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			expectError:          true,
			expectResult:         false,
			expectUpdatedStatus:  false,
			errorContains:        "pipeline not found or not ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if tt.pipeline != nil {
				objects = append(objects, tt.pipeline)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          client,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
					DecisionQueue:   make(chan lib.DecisionUpdate, 100),
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}

			// Setup pipeline configurations if needed
			if tt.setupPipelineConfigs && tt.pipelineConf != nil {
				controller.PipelineConfigs[tt.pipelineConf.Name] = *tt.pipelineConf
			}

			// Setup runtime pipeline if needed
			if tt.pipeline != nil {
				initResult := controller.InitPipeline(context.Background(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.pipeline.Name,
					},
					Spec: tt.pipeline.Spec,
				})
				if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
					t.Fatalf("Failed to initialize pipeline: filter errors: %v, weigher errors: %v", initResult.FilterErrors, initResult.WeigherErrors)
				}
				controller.Pipelines[tt.pipeline.Name] = initResult.Pipeline
			}

			// Call the method under test
			result, err := controller.ProcessRequest(context.Background(), tt.request)

			// Validate error expectations
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tt.errorContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errorContains)) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorContains, err)
			}

			// Validate result and duration expectations
			if tt.expectResult && result == nil {
				t.Error("Expected result to be set but was nil")
			}
			if !tt.expectResult && result != nil {
				t.Error("Expected result to be nil but was set")
			}
		})
	}
}

func TestFilterWeigherPipelineController_IgnorePreselection(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	// Create a request with initial hosts
	novaRequest := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Name:      "RequestSpec",
			Namespace: "nova_object",
			Version:   "1.19",
			Data: api.NovaSpec{
				ProjectID:    "test-project",
				UserID:       "test-user",
				InstanceUUID: "test-instance-uuid",
				NumInstances: 1,
			},
		},
		Context: api.NovaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: func() *string { s := "global-req-123"; return &s }(),
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "original-host-1", HypervisorHostname: "hv-1"},
			{ComputeHost: "original-host-2", HypervisorHostname: "hv-2"},
		},
		Weights:  map[string]float64{"original-host-1": 1.0, "original-host-2": 0.5},
		Pipeline: "test-pipeline",
	}

	tests := []struct {
		name               string
		ignorePreselection bool
		gathererErr        error
		gatheredHosts      []api.ExternalSchedulerHost
		expectGathererCall bool
		expectError        bool
		errorContains      string
	}{
		{
			name:               "IgnorePreselection disabled - gatherer not called",
			ignorePreselection: false,
			gathererErr:        nil,
			gatheredHosts:      nil,
			expectGathererCall: false,
			expectError:        false,
		},
		{
			name:               "IgnorePreselection enabled - gatherer called and succeeds",
			ignorePreselection: true,
			gathererErr:        nil,
			gatheredHosts: []api.ExternalSchedulerHost{
				{ComputeHost: "gathered-host-1", HypervisorHostname: "gathered-host-1"},
				{ComputeHost: "gathered-host-2", HypervisorHostname: "gathered-host-2"},
				{ComputeHost: "gathered-host-3", HypervisorHostname: "gathered-host-3"},
			},
			expectGathererCall: true,
			expectError:        false,
		},
		{
			name:               "IgnorePreselection enabled - gatherer returns error",
			ignorePreselection: true,
			gathererErr:        errGathererFailed,
			gatheredHosts:      nil,
			expectGathererCall: true,
			expectError:        true,
			errorContains:      "gatherer failed",
		},
		{
			name:               "IgnorePreselection enabled - gatherer returns empty hosts",
			ignorePreselection: true,
			gathererErr:        nil,
			gatheredHosts:      []api.ExternalSchedulerHost{},
			expectGathererCall: true,
			expectError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGatherer := &mockCandidateGatherer{
				err:           tt.gathererErr,
				gatheredHosts: tt.gatheredHosts,
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          fakeClient,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
				},
				Monitor:  lib.FilterWeigherPipelineMonitor{},
				gatherer: mockGatherer,
			}

			// Setup pipeline config with IgnorePreselection flag
			pipelineConf := v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:               v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain:   v1alpha1.SchedulingDomainNova,
					CreateDecisions:    false,
					IgnorePreselection: tt.ignorePreselection,
					Filters:            []v1alpha1.FilterSpec{},
					Weighers:           []v1alpha1.WeigherSpec{},
				},
			}
			controller.PipelineConfigs["test-pipeline"] = pipelineConf

			// Initialize the pipeline
			initResult := controller.InitPipeline(context.Background(), pipelineConf)
			if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
				t.Fatalf("Failed to initialize pipeline: filter errors: %v, weigher errors: %v", initResult.FilterErrors, initResult.WeigherErrors)
			}
			controller.Pipelines["test-pipeline"] = initResult.Pipeline

			// Process the decision
			result, err := controller.ProcessRequest(context.Background(), novaRequest)

			// Verify gatherer was called (or not) as expected
			if tt.expectGathererCall && !mockGatherer.called {
				t.Error("Expected gatherer to be called but it was not")
			}
			if !tt.expectGathererCall && mockGatherer.called {
				t.Error("Expected gatherer not to be called but it was")
			}

			// Verify error expectations
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tt.errorContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errorContains)) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorContains, err)
			}

			// Verify result is set when no error
			if !tt.expectError && result == nil {
				t.Error("Expected result to be set but was nil")
			}
		})
	}
}

// Error variable for testing
var errGathererFailed = errors.New("gatherer failed")

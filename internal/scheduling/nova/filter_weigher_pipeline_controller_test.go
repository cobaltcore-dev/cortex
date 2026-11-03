// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
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

func TestFilterWeigherPipelineController_Reconcile(t *testing.T) {
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
		Options:  scheduling.Options{},
	}

	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal nova request: %v", err)
	}

	tests := []struct {
		name         string
		decision     *v1alpha1.Decision
		pipeline     *v1alpha1.Pipeline
		expectError  bool
		expectResult bool
	}{
		{
			name: "successful nova decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
		{
			name: "decision without novaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  true,
			expectResult: false,
		},
		{
			name: "pipeline not found",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline:     nil,
			expectError:  true,
			expectResult: false,
		},
		{
			name: "invalid novaRaw JSON",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-invalid-json",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: []byte("invalid json"),
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  true,
			expectResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.decision}
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
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}

			if tt.pipeline != nil {
				initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.pipeline.Name,
					},
					Spec: tt.pipeline.Spec,
				})
				if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
					t.Fatalf("Failed to initialize pipeline: filter errors: %v, weigher errors: %v", initResult.FilterErrors, initResult.WeigherErrors)
				}
				controller.Pipelines[tt.pipeline.Name] = initResult.Pipeline
				controller.PipelineConfigs[tt.pipeline.Name] = *tt.pipeline
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.decision.Name,
					Namespace: tt.decision.Namespace,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if result.RequeueAfter > 0 {
				t.Error("Expected no requeue")
			}

			var updatedDecision v1alpha1.Decision
			if err := client.Get(context.Background(), req.NamespacedName, &updatedDecision); err != nil {
				if !tt.expectError {
					t.Fatalf("Failed to get updated decision: %v", err)
				}
				return
			}

			if tt.expectResult && updatedDecision.Status.Result == nil {
				t.Error("Expected result to be set but was nil")
			}
			if !tt.expectResult && updatedDecision.Status.Result != nil {
				t.Error("Expected result to be nil but was set")
			}
		})
	}
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
		expectUnknownFilter    bool
		expectUnknownWeigher   bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
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
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "unsupported-plugin",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    true,
			expectUnknownWeigher:   false,
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

func TestFilterWeigherPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
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

	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal nova request: %v", err)
	}

	tests := []struct {
		name                 string
		decision             *v1alpha1.Decision
		pipeline             *v1alpha1.Pipeline
		pipelineConf         *v1alpha1.Pipeline
		setupPipelineConfigs bool
		expectError          bool
		expectResult         bool
		expectUpdatedStatus  bool
		errorContains        string
	}{
		{
			name: "successful processing with decision creation enabled",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-api",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-uuid-1",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
			name: "successful processing with decision creation disabled",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-create",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-uuid-2",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline-no-create",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-no-create",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-no-create",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			expectError:          false,
			expectResult:         true,
			expectUpdatedStatus:  false,
		},
		{
			name: "pipeline not configured",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-config",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline:             nil,
			pipelineConf:         nil,
			setupPipelineConfigs: false,
			expectError:          true,
			expectResult:         false,
			expectUpdatedStatus:  false,
			errorContains:        "pipeline not found or not ready",
		},
		{
			name: "decision without novaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw-api",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			expectError:          true,
			expectResult:         false,
			expectUpdatedStatus:  false,
			errorContains:        "no novaRaw spec defined",
		},
		{
			name: "processing fails after decision creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-process-fail",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: nil, // This will cause processing to fail after creation
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
		{
			name: "pipeline not found in runtime map",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-runtime-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "missing-runtime-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: nil,
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "missing-runtime-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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

			if tt.decision.Spec.NovaRaw != nil {
				req := novaRequest
				req.Options = scheduling.Options{}
				raw, marshalErr := json.Marshal(req)
				if marshalErr != nil {
					t.Fatalf("Failed to marshal request with options: %v", marshalErr)
				}
				tt.decision.Spec.NovaRaw = &runtime.RawExtension{Raw: raw}
			}

			// Call the method under test
			err := controller.ProcessNewDecisionFromAPI(context.Background(), tt.decision)

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

			// Validate result expectations
			if tt.expectResult && tt.decision.Status.Result == nil {
				t.Error("Expected result to be set but was nil")
			}
			if !tt.expectResult && tt.decision.Status.Result != nil {
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
		Options:  scheduling.Options{},
	}

	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal nova request: %v", err)
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

			// Create decision
			decision := &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-preselection",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			}

			// Process the decision
			err := controller.ProcessNewDecisionFromAPI(context.Background(), decision)

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
			if !tt.expectError && decision.Status.Result == nil {
				t.Error("Expected result to be set but was nil")
			}
		})
	}
}

// Error variable for testing
var errGathererFailed = errors.New("gatherer failed")

func TestFilterWeigherPipelineController_PeekReadOnly(t *testing.T) {
	makeRaw := func(readOnly bool) []byte {
		r := api.ExternalSchedulerRequest{
			Spec:    api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{NumInstances: 1}},
			Options: scheduling.Options{ReadOnly: readOnly},
		}
		raw, err := json.Marshal(r)
		if err != nil {
			panic(err)
		}
		return raw
	}

	c := &FilterWeigherPipelineController{}

	tests := []struct {
		name     string
		decision *v1alpha1.Decision
		want     bool
	}{
		{
			name: "nil NovaRaw defaults to exclusive lock",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					PipelineRef: corev1.ObjectReference{Name: "test-pipeline"},
				},
			},
			want: false,
		},
		{
			name: "invalid JSON defaults to exclusive lock",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					PipelineRef: corev1.ObjectReference{Name: "test-pipeline"},
					NovaRaw:     &runtime.RawExtension{Raw: []byte("not-json")},
				},
			},
			want: false,
		},
		{
			name: "ReadOnly=false uses exclusive lock",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					PipelineRef: corev1.ObjectReference{Name: "test-pipeline"},
					NovaRaw:     &runtime.RawExtension{Raw: makeRaw(false)},
				},
			},
			want: false,
		},
		{
			name: "ReadOnly=true uses read lock",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					PipelineRef: corev1.ObjectReference{Name: "test-pipeline"},
					NovaRaw:     &runtime.RawExtension{Raw: makeRaw(true)},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.peekReadOnly(tt.decision)
			if got != tt.want {
				t.Errorf("expected peekReadOnly = %v, got %v", tt.want, got)
			}
		})
	}
}

// fakeFailoverMatcher records the candidates it was handed and returns a
// preconfigured result, so controller wiring can be asserted in isolation.
type fakeFailoverMatcher struct {
	called   bool
	gotHosts []string
	ret      []string
	err      error
}

func (f *fakeFailoverMatcher) Match(_ context.Context, _ api.ExternalSchedulerRequest, originalHosts []string) ([]string, error) {
	f.called = true
	f.gotHosts = originalHosts
	if f.err != nil {
		return nil, f.err
	}
	return f.ret, nil
}

// runProcessWithMatcher builds a controller with an empty pipeline and the given
// matcher, runs the decision through ProcessNewDecisionFromAPI, and returns the
// decision and any error. The request's weights make the pipeline order the two
// hosts [compute-1, compute-2].
func runProcessWithMatcher(t *testing.T, matcher FailoverHostMatcher, readOnly bool) (*v1alpha1.Decision, error) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}

	req := api.ExternalSchedulerRequest{
		Spec:     api.NovaObject[api.NovaSpec]{Data: api.NovaSpec{InstanceUUID: "vm-1", NumInstances: 1}},
		Hosts:    []api.ExternalSchedulerHost{{ComputeHost: "compute-1"}, {ComputeHost: "compute-2"}},
		Weights:  map[string]float64{"compute-1": 1.0, "compute-2": 0.5},
		Pipeline: "p",
	}
	if readOnly {
		// ReadOnly runs must also skip inflight and CR tracking, else Options.Validate rejects them.
		req.Options = scheduling.Options{ReadOnly: true, SkipInflight: true, SkipCommittedResourceTracking: true}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: v1alpha1.PipelineSpec{
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&v1alpha1.Decision{}).
		Build()
	controller := &FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
			Client:          fakeClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor:         lib.FilterWeigherPipelineMonitor{},
		FailoverMatcher: matcher,
	}
	controller.PipelineConfigs["p"] = *pipeline
	initResult := controller.InitPipeline(context.Background(), *pipeline)
	if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
		t.Fatalf("init pipeline: %v %v", initResult.FilterErrors, initResult.WeigherErrors)
	}
	controller.Pipelines["p"] = initResult.Pipeline

	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "default"},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			PipelineRef:      corev1.ObjectReference{Name: "p"},
			NovaRaw:          &runtime.RawExtension{Raw: raw},
		},
	}
	return decision, controller.ProcessNewDecisionFromAPI(context.Background(), decision)
}

func TestFilterWeigherPipelineController_FailoverMatcherRewritesResult(t *testing.T) {
	m := &fakeFailoverMatcher{ret: []string{"compute-2"}}
	decision, err := runProcessWithMatcher(t, m, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.called {
		t.Fatal("expected matcher to be called")
	}
	// Matcher receives the pipeline's ordered hosts.
	if len(m.gotHosts) != 2 || m.gotHosts[0] != "compute-1" || m.gotHosts[1] != "compute-2" {
		t.Errorf("matcher got unexpected hosts: %v", m.gotHosts)
	}
	// Its returned hosts become the result, with TargetHost pointing at the first.
	if decision.Status.Result == nil {
		t.Fatal("expected result to be set")
	}
	if got := decision.Status.Result.OrderedHosts; len(got) != 1 || got[0] != "compute-2" {
		t.Errorf("expected OrderedHosts [compute-2], got %v", got)
	}
	if decision.Status.Result.TargetHost == nil || *decision.Status.Result.TargetHost != "compute-2" {
		t.Errorf("expected TargetHost compute-2, got %v", decision.Status.Result.TargetHost)
	}
}

func TestFilterWeigherPipelineController_FailoverMatcherSkippedOnReadOnly(t *testing.T) {
	m := &fakeFailoverMatcher{ret: []string{"compute-2"}}
	decision, err := runProcessWithMatcher(t, m, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.called {
		t.Error("expected matcher NOT to be called on a read-only run")
	}
	// The pipeline's original ordering is preserved.
	if got := decision.Status.Result.OrderedHosts; len(got) != 2 || got[0] != "compute-1" {
		t.Errorf("expected original ordering, got %v", got)
	}
}

func TestFilterWeigherPipelineController_FailoverMatcherErrorFailsDecision(t *testing.T) {
	m := &fakeFailoverMatcher{err: errors.New("patch failed")}
	decision, err := runProcessWithMatcher(t, m, false)
	if err == nil || !strings.Contains(err.Error(), "patch failed") {
		t.Fatalf("expected matcher error to propagate, got %v", err)
	}
	// On matcher failure the decision carries no result.
	if decision.Status.Result != nil {
		t.Errorf("expected no result on matcher error, got %v", decision.Status.Result)
	}
}

func TestFilterWeigherPipelineController_FailoverMatcherEmptyClearsTargetHost(t *testing.T) {
	m := &fakeFailoverMatcher{ret: []string{}}
	decision, err := runProcessWithMatcher(t, m, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Status.Result.TargetHost != nil {
		t.Errorf("expected nil TargetHost for empty host list, got %v", *decision.Status.Result.TargetHost)
	}
}

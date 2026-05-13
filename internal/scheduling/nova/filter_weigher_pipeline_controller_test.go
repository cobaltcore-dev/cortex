// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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
		createHistory        bool
		expectError          bool
		expectResult         bool
		expectHistoryCreated bool
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
					CreateHistory:    true,
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
					CreateHistory:    true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			createHistory:        true,
			expectError:          false,
			expectResult:         true,
			expectHistoryCreated: true,
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
					CreateHistory:    false,
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
					CreateHistory:    false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			createHistory:        false,
			expectError:          false,
			expectResult:         true,
			expectHistoryCreated: false,
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
			expectHistoryCreated: false,
			expectUpdatedStatus:  false,
			errorContains:        "pipeline nonexistent-pipeline not configured",
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
					CreateHistory:    true,
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
					CreateHistory:    true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			createHistory:        true,
			expectError:          true,
			expectResult:         false,
			expectHistoryCreated: true,
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
					CreateHistory:    true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			createHistory:        true,
			expectError:          true,
			expectResult:         false,
			expectHistoryCreated: true,
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
					CreateHistory:    true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs: true,
			createHistory:        true,
			expectError:          true,
			expectResult:         false,
			expectHistoryCreated: true,
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
				WithStatusSubresource(&v1alpha1.Decision{}, &v1alpha1.History{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          client,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
					HistoryManager:  lib.HistoryClient{Client: client},
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

			// Check if history CRD was created when expected
			if tt.expectHistoryCreated {
				var histories v1alpha1.HistoryList
				deadline := time.Now().Add(2 * time.Second)
				for {
					if err := client.List(context.Background(), &histories); err != nil {
						t.Fatalf("Failed to list histories: %v", err)
					}
					if len(histories.Items) > 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("timed out waiting for history CRD to be created")
					}
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				var histories v1alpha1.HistoryList
				if err := client.List(context.Background(), &histories); err != nil {
					t.Fatalf("Failed to list histories: %v", err)
				}
				if len(histories.Items) != 0 {
					t.Error("Expected no history CRD but found one")
				}
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
					CreateHistory:      false,
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

func TestIsUserVMPlacement(t *testing.T) {
	tests := []struct {
		intent   v1alpha1.SchedulingIntent
		expected bool
	}{
		{api.CreateIntent, true},
		{api.LiveMigrationIntent, true},
		{api.EvacuateIntent, true},
		{api.RebuildIntent, true},
		{api.ResizeIntent, true},
		{api.ReserveForCommittedResourceIntent, false},
		{api.ReserveForFailoverIntent, false},
		{v1alpha1.SchedulingIntentUnknown, true},
	}
	for _, tt := range tests {
		if got := isUserVMPlacement(tt.intent); got != tt.expected {
			t.Errorf("isUserVMPlacement(%q) = %v, want %v", tt.intent, got, tt.expected)
		}
	}
}

func TestPickReservationSlot(t *testing.T) {
	// vmMemBytes and vmCPUs for a 4096 MiB / 2 vCPU flavor.
	const (
		vmMemBytes = int64(4096) * 1024 * 1024
		vmCPUs     = int64(2)
	)

	makeSlot := func(name string, totalMemMiB, totalCPU, usedMemMiB, usedCPU int64) v1alpha1.Reservation {
		var allocs map[string]v1alpha1.CommittedResourceAllocation
		if usedMemMiB > 0 || usedCPU > 0 {
			allocs = map[string]v1alpha1.CommittedResourceAllocation{
				"vm-existing": {
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: *resource.NewQuantity(usedMemMiB*1024*1024, resource.BinarySI),
						hv1.ResourceCPU:    *resource.NewQuantity(usedCPU, resource.DecimalSI),
					},
				},
			}
		}
		return v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(totalMemMiB*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(totalCPU, resource.DecimalSI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Allocations: allocs,
				},
			},
		}
	}

	tests := []struct {
		name       string
		candidates []v1alpha1.Reservation
		want       string
	}{
		{
			name:       "no candidates",
			candidates: nil,
			want:       "",
		},
		{
			name:       "single slot fits",
			candidates: []v1alpha1.Reservation{makeSlot("a", 8192, 8, 0, 0)},
			want:       "a",
		},
		{
			name:       "single slot too small after allocations",
			candidates: []v1alpha1.Reservation{makeSlot("a", 8192, 8, 8192, 8)}, // fully consumed
			want:       "",
		},
		{
			name: "picks slot with least remaining memory",
			candidates: []v1alpha1.Reservation{
				makeSlot("large", 8192, 8, 0, 0), // 8192 MiB remaining
				makeSlot("small", 6144, 8, 0, 0), // 6144 MiB remaining, still fits
			},
			want: "small",
		},
		{
			name: "CPU tiebreak on equal remaining memory",
			candidates: []v1alpha1.Reservation{
				makeSlot("more-cpu", 6144, 8, 0, 0), // remCPU = 8
				makeSlot("less-cpu", 6144, 4, 0, 0), // remCPU = 4
			},
			want: "less-cpu",
		},
		{
			name: "name tiebreak on equal remaining memory and CPU",
			candidates: []v1alpha1.Reservation{
				makeSlot("slot-b", 6144, 4, 0, 0),
				makeSlot("slot-a", 6144, 4, 0, 0),
			},
			want: "slot-a",
		},
		{
			name: "missing resource keys treated as zero remaining",
			candidates: []v1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "empty-res"},
					Spec: v1alpha1.ReservationSpec{
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{},
					},
				},
			},
			want: "",
		},
		{
			name:       "partially used slot still fits",
			candidates: []v1alpha1.Reservation{makeSlot("partial", 8192, 8, 2048, 2)}, // 6144 MiB remaining
			want:       "partial",
		},
		{
			name:       "CPU exhausted: slot excluded as hard constraint",
			candidates: []v1alpha1.Reservation{makeSlot("cpu-full", 8192, 2, 0, 2)}, // remCPU = 0
			want:       "",
		},
		{
			name: "CPU exhausted slot skipped, other slot chosen",
			candidates: []v1alpha1.Reservation{
				makeSlot("cpu-full", 8192, 2, 0, 2), // remCPU = 0, excluded
				makeSlot("cpu-ok", 8192, 4, 0, 0),   // remCPU = 4, fits
			},
			want: "cpu-ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickReservationSlot(tt.candidates, vmMemBytes, vmCPUs)
			if got != tt.want {
				t.Errorf("pickReservationSlot() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordCRAllocation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	const (
		instanceUUID = "vm-uuid-1"
		projectID    = "project-1"
		flavorName   = "m1.large"
		flavorGroup  = "m1"
		selectedHost = "compute-1"
	)

	ratio := uint64(2048)
	fg := compute.FlavorGroupFeature{
		Name:           flavorGroup,
		Flavors:        []compute.FlavorInGroup{{Name: flavorName, VCPUs: 2, MemoryMB: 4096}},
		LargestFlavor:  compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		SmallestFlavor: compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		RamCoreRatio:   &ratio,
	}

	flavorKnowledge := func() *v1alpha1.Knowledge {
		raw, err := v1alpha1.BoxFeatureList([]compute.FlavorGroupFeature{fg})
		if err != nil {
			t.Fatalf("BoxFeatureList: %v", err)
		}
		return &v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
			Status: v1alpha1.KnowledgeStatus{
				Raw: raw,
				Conditions: []metav1.Condition{{
					Type:               v1alpha1.KnowledgeConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.Now(),
				}},
			},
		}
	}

	makeReservation := func(name string, memMiB, cpus int64, proj, group, host string, allocs map[string]v1alpha1.CommittedResourceAllocation) *v1alpha1.Reservation {
		return &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: host,
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(memMiB*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(cpus, resource.DecimalSI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     proj,
					ResourceGroup: group,
					Allocations:   allocs,
				},
			},
		}
	}

	makeRequest := func(uuid, proj, flavor string) api.ExternalSchedulerRequest {
		return api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					InstanceUUID: uuid,
					Flavor: api.NovaObject[api.NovaFlavor]{
						Data: api.NovaFlavor{Name: flavor, MemoryMB: 4096, VCPUs: 2},
					},
				},
			},
			Context: api.NovaRequestContext{ProjectID: proj},
		}
	}

	makeDecision := func(host string) *v1alpha1.Decision {
		h := host
		return &v1alpha1.Decision{
			Status: v1alpha1.DecisionStatus{
				Result: &v1alpha1.DecisionResult{TargetHost: &h},
			},
		}
	}

	vmAlloc := func() map[string]v1alpha1.CommittedResourceAllocation {
		return map[string]v1alpha1.CommittedResourceAllocation{
			instanceUUID: {
				CreationTimestamp: metav1.Now(),
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(int64(4096)*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		}
	}

	tests := []struct {
		name             string
		objects          []client.Object
		request          api.ExternalSchedulerRequest
		decision         *v1alpha1.Decision
		checkSlot        string
		expectAllocation bool
	}{
		{
			name: "writes allocation into matching reservation",
			objects: []client.Object{
				flavorKnowledge(),
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: true,
		},
		{
			name: "idempotent: UUID already in allocations",
			objects: []client.Object{
				flavorKnowledge(),
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, vmAlloc()),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: true,
		},
		{
			name: "PAYG: flavor not in any group",
			objects: []client.Object{
				flavorKnowledge(),
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil),
			},
			request:          makeRequest(instanceUUID, projectID, "unknown-flavor"),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
		{
			name: "no matching reservation: host mismatch",
			objects: []client.Object{
				flavorKnowledge(),
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, "other-host", nil),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
		{
			name: "no slot fits: all capacity used",
			objects: []client.Object{
				flavorKnowledge(),
				makeReservation("slot-full", 4096, 2, projectID, flavorGroup, selectedHost,
					map[string]v1alpha1.CommittedResourceAllocation{
						"other-vm": {
							Resources: map[hv1.ResourceName]resource.Quantity{
								hv1.ResourceMemory: *resource.NewQuantity(int64(4096)*1024*1024, resource.BinarySI),
								hv1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
							},
						},
					}),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-full",
			expectAllocation: false,
		},
		{
			name: "no knowledge CRD: logs error, no allocation",
			objects: []client.Object{
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client: fakeClient,
				},
			}

			controller.recordCRAllocation(context.Background(), tt.decision, tt.request)

			var res v1alpha1.Reservation
			if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: tt.checkSlot}, &res); err != nil {
				t.Fatalf("Get reservation %q: %v", tt.checkSlot, err)
			}
			_, hasAlloc := res.Spec.CommittedResourceReservation.Allocations[instanceUUID]
			if tt.expectAllocation && !hasAlloc {
				t.Errorf("expected allocation for UUID %q but none found", instanceUUID)
			}
			if !tt.expectAllocation && hasAlloc {
				t.Errorf("expected no allocation for UUID %q but one was written", instanceUUID)
			}
		})
	}
}

// Error variable for testing
var errGathererFailed = errors.New("gatherer failed")

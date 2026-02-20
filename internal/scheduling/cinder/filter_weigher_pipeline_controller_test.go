// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/cinder"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterWeigherPipelineController_ProcessRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		pipelineConfig *v1alpha1.Pipeline
		expectError    bool
		expectResult   bool
	}{
		{
			name: "successful request processing",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume-id",
					"size":      10,
				},
				Context: api.CinderRequestContext{
					ProjectID:       "test-project",
					UserID:          "test-user",
					RequestID:       "req-123",
					GlobalRequestID: "global-req-123",
					ResourceUUID:    "test-volume-id",
				},
				Hosts: []api.ExternalSchedulerHost{
					{VolumeHost: "cinder-volume-1"},
					{VolumeHost: "cinder-volume-2"},
				},
				Weights:  map[string]float64{"cinder-volume-1": 1.0, "cinder-volume-2": 0.5},
				Pipeline: "test-pipeline",
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
		{
			name: "pipeline not configured",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume-id",
				},
				Context: api.CinderRequestContext{
					ResourceUUID: "test-volume-id",
				},
				Hosts:    []api.ExternalSchedulerHost{{VolumeHost: "cinder-volume-1"}},
				Weights:  map[string]float64{"cinder-volume-1": 1.0},
				Pipeline: "nonexistent-pipeline",
			},
			pipelineConfig: nil,
			expectError:    true,
			expectResult:   false,
		},
		{
			name: "empty hosts",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume-id",
				},
				Context: api.CinderRequestContext{
					ResourceUUID: "test-volume-id",
				},
				Hosts:    []api.ExternalSchedulerHost{},
				Weights:  map[string]float64{},
				Pipeline: "test-pipeline",
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if tt.pipelineConfig != nil {
				objects = append(objects, tt.pipelineConfig)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          fakeClient,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
					DecisionQueue:   make(chan lib.DecisionUpdate, 10),
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				initResult := controller.InitPipeline(context.Background(), *tt.pipelineConfig)
				if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
					t.Fatalf("Failed to init pipeline: %v", initResult)
				}
				controller.Pipelines[tt.pipelineConfig.Name] = initResult.Pipeline
			}

			result, err := controller.ProcessRequest(context.Background(), tt.request)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectResult {
				if result == nil {
					t.Error("Expected result but got nil")
				} else if len(result.OrderedHosts) == 0 && len(tt.request.Hosts) > 0 {
					t.Error("Expected ordered hosts in result")
				}
			}
		})
	}
}

func TestFilterWeigherPipelineController_PipelineType(t *testing.T) {
	controller := &FilterWeigherPipelineController{}

	pipelineType := controller.PipelineType()

	if pipelineType != v1alpha1.PipelineTypeFilterWeigher {
		t.Errorf("expected pipeline type %s, got %s", v1alpha1.PipelineTypeFilterWeigher, pipelineType)
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
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "test-plugin",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true, // Expected because test-plugin is not in supportedSteps
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initResult := controller.InitPipeline(context.Background(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Filters:          tt.filters,
					Weighers:         tt.weighers,
				},
			})

			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("Expected error but got none")
			}
			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("Expected no error but got: %v", initResult.FilterErrors)
			}

			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("Expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("Expected no non-critical error but got: %v", initResult.WeigherErrors)
			}
		})
	}
}

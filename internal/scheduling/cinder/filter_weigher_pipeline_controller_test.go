// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/cinder"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestFilterWeigherPipelineController_Schedule(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{VolumeHost: "cinder-volume-1"},
			{VolumeHost: "cinder-volume-2"},
		},
		Weights:  map[string]float64{"cinder-volume-1": 1.0, "cinder-volume-2": 0.5},
		Pipeline: "test-pipeline",
		Options:  scheduling.Options{},
	}

	tests := []struct {
		name         string
		pipelineName string
		configured   bool
		expectError  bool
	}{
		{
			name:         "successful scheduling run",
			pipelineName: "test-pipeline",
			configured:   true,
			expectError:  false,
		},
		{
			name:         "pipeline not found",
			pipelineName: "nonexistent-pipeline",
			configured:   false,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:    cl,
					Pipelines: make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}

			if tt.configured {
				initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
					Spec: v1alpha1.PipelineSpec{
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						SchedulingDomain: v1alpha1.SchedulingDomainCinder,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				})
				if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
					t.Fatalf("Failed to init pipeline: %v", initResult)
				}
				controller.Pipelines["test-pipeline"] = initResult.Pipeline
			}

			result, explanation, err := controller.Schedule(t.Context(), tt.pipelineName, request)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if !tt.expectError {
				if len(result.OrderedHosts) == 0 {
					t.Error("Expected ordered hosts but got none")
				}
				if explanation == "" {
					t.Error("Expected a non-empty explanation")
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
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "test-plugin",
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

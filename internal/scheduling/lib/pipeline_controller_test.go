// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

func TestBasePipelineController_InitAllPipelines(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		existingPipelines []v1alpha1.Pipeline
		schedulingDomain  v1alpha1.SchedulingDomain
		pipelineType      v1alpha1.PipelineType
		expectedCount     int
		expectError       bool
	}{
		{
			name:              "no existing pipelines",
			existingPipelines: []v1alpha1.Pipeline{},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			pipelineType:      v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:     0,
			expectError:       false,
		},
		{
			name: "one matching pipeline",
			existingPipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			pipelineType:     v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:    1,
			expectError:      false,
		},
		{
			name: "multiple pipelines, only some matching",
			existingPipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "matching-pipeline-1",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "different-domain-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainCinder,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "different-type-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeDetector,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "matching-pipeline-2",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Filters:          []v1alpha1.FilterSpec{},
						Weighers:         []v1alpha1.WeigherSpec{},
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			pipelineType:     v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:    2,
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, len(tt.existingPipelines))
			for i := range tt.existingPipelines {
				objects[i] = &tt.existingPipelines[i]
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer: &mockPipelineInitializer{
					pipelineType: tt.pipelineType,
				},
			}

			err := controller.InitAllPipelines(context.Background())

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(controller.Pipelines) != tt.expectedCount {
				t.Errorf("Expected %d pipelines, got %d", tt.expectedCount, len(controller.Pipelines))
			}

			if len(controller.PipelineConfigs) != tt.expectedCount {
				t.Errorf("Expected %d pipeline configs, got %d", tt.expectedCount, len(controller.PipelineConfigs))
			}
		})
	}
}

func TestBasePipelineController_handlePipelineChange(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name                  string
		pipeline              *v1alpha1.Pipeline
		knowledges            []v1alpha1.Knowledge
		schedulingDomain      v1alpha1.SchedulingDomain
		initPipelineError     bool
		expectReady           bool
		expectInMap           bool
		expectAllStepsIndexed *bool
		unknownFilters        []string
		unknownWeighers       []string
		unknownDetectors      []string
	}{
		{
			name: "pipeline with all steps ready",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{
							Name: "test-filter",
						},
					},
					Weighers: []v1alpha1.WeigherSpec{
						{
							Name: "test-weigher",
						},
					},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge-1",
						Namespace: "default",
					},
					Spec: v1alpha1.KnowledgeSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
					},
				},
			},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           true,
			expectInMap:           true,
			expectAllStepsIndexed: testlib.Ptr(true),
		},
		{
			name: "pipeline init fails",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-init-fail",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			initPipelineError:     true,
			expectReady:           false,
			expectInMap:           false,
			expectAllStepsIndexed: nil, // Not set when filter init fails
		},
		{
			name: "pipeline with different scheduling domain",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-different-domain",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           false,
			expectInMap:           false,
			expectAllStepsIndexed: nil, // Not set for different domain
		},
		{
			name: "pipeline with unknown filters",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-unknown-filter",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{
							Name: "unknown-filter",
						},
					},
					Weighers: []v1alpha1.WeigherSpec{},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           true,
			expectInMap:           true,
			expectAllStepsIndexed: testlib.Ptr(false),
			unknownFilters:        []string{"unknown-filter"},
		},
		{
			name: "pipeline with unknown weighers",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-unknown-weigher",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers: []v1alpha1.WeigherSpec{
						{
							Name: "unknown-weigher",
						},
					},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           true,
			expectInMap:           true,
			expectAllStepsIndexed: testlib.Ptr(false),
			unknownWeighers:       []string{"unknown-weigher"},
		},
		{
			name: "pipeline with unknown detectors",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-unknown-detector",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeDetector,
					Detectors: []v1alpha1.DetectorSpec{
						{
							Name: "unknown-detector",
						},
					},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           true,
			expectInMap:           true,
			expectAllStepsIndexed: testlib.Ptr(false),
			unknownDetectors:      []string{"unknown-detector"},
		},
		{
			name: "pipeline with multiple unknown steps",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-multiple-unknown",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters: []v1alpha1.FilterSpec{
						{
							Name: "unknown-filter-1",
						},
						{
							Name: "unknown-filter-2",
						},
					},
					Weighers: []v1alpha1.WeigherSpec{
						{
							Name: "unknown-weigher-1",
						},
					},
				},
			},
			knowledges:            []v1alpha1.Knowledge{},
			schedulingDomain:      v1alpha1.SchedulingDomainNova,
			expectReady:           true,
			expectInMap:           true,
			expectAllStepsIndexed: testlib.Ptr(false),
			unknownFilters:        []string{"unknown-filter-1", "unknown-filter-2"},
			unknownWeighers:       []string{"unknown-weigher-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.pipeline}
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			initializer := &mockPipelineInitializer{
				pipelineType: tt.pipeline.Spec.Type,
			}

			if tt.initPipelineError {
				initializer.initPipelineFunc = func(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[mockPipeline] {
					return PipelineInitResult[mockPipeline]{
						FilterErrors: map[string]error{
							"test-filter": errors.New("failed to init filter"),
						},
					}
				}
			} else if len(tt.unknownFilters) > 0 || len(tt.unknownWeighers) > 0 || len(tt.unknownDetectors) > 0 {
				initializer.initPipelineFunc = func(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[mockPipeline] {
					return PipelineInitResult[mockPipeline]{
						Pipeline:         mockPipeline{name: p.Name},
						UnknownFilters:   tt.unknownFilters,
						UnknownWeighers:  tt.unknownWeighers,
						UnknownDetectors: tt.unknownDetectors,
					}
				}
			}

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer:      initializer,
				Pipelines:        make(map[string]mockPipeline),
				PipelineConfigs:  make(map[string]v1alpha1.Pipeline),
			}

			controller.handlePipelineChange(context.Background(), tt.pipeline, nil)

			// Check if pipeline is in map
			_, inMap := controller.Pipelines[tt.pipeline.Name]
			if inMap != tt.expectInMap {
				t.Errorf("Expected pipeline in map: %v, got: %v", tt.expectInMap, inMap)
			}

			// Get updated pipeline status
			var updatedPipeline v1alpha1.Pipeline
			err := fakeClient.Get(context.Background(), client.ObjectKey{Name: tt.pipeline.Name}, &updatedPipeline)
			if err != nil {
				t.Fatalf("Failed to get updated pipeline: %v", err)
			}

			// Check ready status
			ready := meta.IsStatusConditionTrue(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionReady)
			if ready != tt.expectReady {
				t.Errorf("Expected ready status: %v, got: %v", tt.expectReady, ready)
			}

			// Check AllStepsIndexed status
			if tt.expectAllStepsIndexed != nil {
				allStepsIndexed := meta.IsStatusConditionTrue(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionAllStepsIndexed)
				if allStepsIndexed != *tt.expectAllStepsIndexed {
					t.Errorf("Expected AllStepsIndexed status: %v, got: %v", *tt.expectAllStepsIndexed, allStepsIndexed)
				}

				// Verify the condition message contains the unknown steps when applicable
				if !*tt.expectAllStepsIndexed {
					condition := meta.FindStatusCondition(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionAllStepsIndexed)
					if condition == nil {
						t.Error("Expected AllStepsIndexed condition to be set")
					} else {
						if condition.Reason != "PipelineContainsUnknownSteps" {
							t.Errorf("Expected reason 'PipelineContainsUnknownSteps', got: %s", condition.Reason)
						}
						// Check that unknown steps are mentioned in the message
						totalUnknown := len(tt.unknownFilters) + len(tt.unknownWeighers) + len(tt.unknownDetectors)
						expectedMsg := fmt.Sprintf("pipeline contains %d unknown steps:", totalUnknown)
						if !strings.Contains(condition.Message, expectedMsg) {
							t.Errorf("Expected message to contain '%s', got: %s", expectedMsg, condition.Message)
						}
					}
				} else {
					condition := meta.FindStatusCondition(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionAllStepsIndexed)
					if condition == nil {
						t.Error("Expected AllStepsIndexed condition to be set")
					} else if condition.Reason != "AllStepsIndexed" {
						t.Errorf("Expected reason 'AllStepsIndexed', got: %s", condition.Reason)
					}
				}
			}
		})
	}
}

func TestBasePipelineController_HandlePipelineCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Filters:          []v1alpha1.FilterSpec{},
			Weighers:         []v1alpha1.WeigherSpec{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.CreateEvent{
		Object: pipeline,
	}

	controller.HandlePipelineCreated(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[pipeline.Name]; !exists {
		t.Error("Expected pipeline to be in map after creation")
	}
}

func TestBasePipelineController_HandlePipelineUpdated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	oldPipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Filters:          []v1alpha1.FilterSpec{},
			Weighers:         []v1alpha1.WeigherSpec{},
		},
	}

	newPipeline := oldPipeline.DeepCopy()
	newPipeline.Spec.Description = "Updated description"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newPipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.UpdateEvent{
		ObjectOld: oldPipeline,
		ObjectNew: newPipeline,
	}

	controller.HandlePipelineUpdated(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[newPipeline.Name]; !exists {
		t.Error("Expected pipeline to be in map after update")
	}
}

func TestBasePipelineController_HandlePipelineDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
		},
	}

	controller := &BasePipelineController[mockPipeline]{
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		PipelineConfigs: map[string]v1alpha1.Pipeline{
			"test-pipeline": *pipeline,
		},
	}

	evt := event.DeleteEvent{
		Object: pipeline,
	}

	controller.HandlePipelineDeleted(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[pipeline.Name]; exists {
		t.Error("Expected pipeline to be removed from map after deletion")
	}
	if _, exists := controller.PipelineConfigs[pipeline.Name]; exists {
		t.Error("Expected pipeline config to be removed from map after deletion")
	}
}

func TestBasePipelineController_handleKnowledgeChange(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		knowledge         *v1alpha1.Knowledge
		pipelines         []v1alpha1.Pipeline
		schedulingDomain  v1alpha1.SchedulingDomain
		expectReEvaluated []string
	}{
		{
			name: "knowledge change triggers pipeline re-evaluation",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipeline-1",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Weighers: []v1alpha1.WeigherSpec{
							{
								Name: "test-weigher",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipeline-2",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Weighers: []v1alpha1.WeigherSpec{
							{
								Name: "test-weigher",
							},
						},
					},
				},
			},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			expectReEvaluated: []string{"pipeline-1", "pipeline-2"},
		},
		{
			name: "knowledge change in different scheduling domain",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
				},
			},
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nova-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Weighers: []v1alpha1.WeigherSpec{
							{
								Name: "test-weigher",
							},
						},
					},
				},
			},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			expectReEvaluated: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.knowledge}
			for i := range tt.pipelines {
				objects = append(objects, &tt.pipelines[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer: &mockPipelineInitializer{
					pipelineType: v1alpha1.PipelineTypeFilterWeigher,
				},
				Pipelines:       make(map[string]mockPipeline),
				PipelineConfigs: make(map[string]v1alpha1.Pipeline),
			}

			controller.handleKnowledgeChange(context.Background(), tt.knowledge, nil)

			// Verify expected pipelines were re-evaluated by checking if they're in the map
			for _, expectedName := range tt.expectReEvaluated {
				if _, exists := controller.Pipelines[expectedName]; !exists {
					t.Errorf("Expected pipeline %s to be re-evaluated", expectedName)
				}
			}
		})
	}
}

func TestBasePipelineController_HandleKnowledgeCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-knowledge",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
		Status: v1alpha1.KnowledgeStatus{
			RawLength: 10,
		},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Weighers: []v1alpha1.WeigherSpec{
				{
					Name: "test-weigher",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.CreateEvent{
		Object: knowledge,
	}

	controller.HandleKnowledgeCreated(context.Background(), evt, nil)

	// Pipeline should be re-evaluated and added to map
	if _, exists := controller.Pipelines[pipeline.Name]; !exists {
		t.Error("Expected pipeline to be re-evaluated after knowledge creation")
	}
}

func TestBasePipelineController_HandleKnowledgeUpdated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name             string
		oldKnowledge     *v1alpha1.Knowledge
		newKnowledge     *v1alpha1.Knowledge
		expectReEvaluate bool
	}{
		{
			name: "error state changed",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.KnowledgeConditionReady,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			expectReEvaluate: true,
		},
		{
			name: "data became available",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 0,
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			expectReEvaluate: true,
		},
		{
			name: "no relevant change",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 15,
				},
			},
			expectReEvaluate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Weighers: []v1alpha1.WeigherSpec{
						{
							Name: "test-weigher",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.newKnowledge, pipeline).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				Initializer: &mockPipelineInitializer{
					pipelineType: v1alpha1.PipelineTypeFilterWeigher,
				},
				Pipelines:       make(map[string]mockPipeline),
				PipelineConfigs: make(map[string]v1alpha1.Pipeline),
			}

			evt := event.UpdateEvent{
				ObjectOld: tt.oldKnowledge,
				ObjectNew: tt.newKnowledge,
			}

			controller.HandleKnowledgeUpdated(context.Background(), evt, nil)

			_, exists := controller.Pipelines[pipeline.Name]
			if tt.expectReEvaluate && !exists {
				t.Error("Expected pipeline to be re-evaluated")
			}
			if !tt.expectReEvaluate && exists {
				t.Error("Expected pipeline not to be re-evaluated")
			}
		})
	}
}

func TestBasePipelineController_HandleKnowledgeDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-knowledge",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Weighers: []v1alpha1.WeigherSpec{
				{
					Name: "test-weigher",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.DeleteEvent{
		Object: knowledge,
	}

	controller.HandleKnowledgeDeleted(context.Background(), evt, nil)

	// Check that the pipeline was re-evaluated and is still in the map
	if _, exists := controller.Pipelines[pipeline.Name]; !exists {
		t.Error("Expected pipeline to be re-evaluated after knowledge deletion")
	}
}

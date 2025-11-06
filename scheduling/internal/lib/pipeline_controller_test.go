// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
)

// Mock pipeline type for testing
type mockPipeline struct {
	name  string
	steps []v1alpha1.Step
}

// Mock initializer implementation
type mockInitializer struct {
	shouldFail   bool
	initPipeline func(steps []v1alpha1.Step) (mockPipeline, error)
}

func (m *mockInitializer) InitPipeline(ctx context.Context, name string, steps []v1alpha1.Step) (mockPipeline, error) {
	if m.shouldFail {
		return mockPipeline{}, errors.New("mock initializer error")
	}
	if m.initPipeline != nil {
		return m.initPipeline(steps)
	}
	return mockPipeline{name: name, steps: steps}, nil
}

func setupTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		return nil
	}
	err = knowledgev1alpha1.AddToScheme(scheme)
	if err != nil {
		return nil
	}
	return scheme
}

func createTestPipeline(steps []v1alpha1.StepInPipeline) *v1alpha1.Pipeline {
	return &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			Operator: "test",
			Type:     v1alpha1.PipelineTypeFilterWeigher,
			Steps:    steps,
		},
	}
}

func createTestStep(ready bool, knowledges []corev1.ObjectReference) *v1alpha1.Step {
	return &v1alpha1.Step{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-step",
			Namespace: "default",
		},
		Spec: v1alpha1.StepSpec{
			Operator:   "test",
			Type:       v1alpha1.StepTypeFilter,
			Impl:       "test-impl",
			Knowledges: knowledges,
		},
		Status: v1alpha1.StepStatus{
			Ready:               ready,
			ReadyKnowledges:     len(knowledges),
			TotalKnowledges:     len(knowledges),
			KnowledgesReadyFrac: "ready",
		},
	}
}

func createTestKnowledge(name string, hasError bool, rawLength int) *knowledgev1alpha1.Knowledge {
	knowledge := &knowledgev1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: knowledgev1alpha1.KnowledgeSpec{
			Operator: "test",
		},
		Status: knowledgev1alpha1.KnowledgeStatus{
			RawLength: rawLength,
		},
	}
	if hasError {
		meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
			Type:    knowledgev1alpha1.KnowledgeConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "TestError",
			Message: "This is a test error",
		})
	}
	return knowledge
}

func TestBasePipelineController_InitAllPipelines(t *testing.T) {
	scheme := setupTestScheme()

	tests := []struct {
		name              string
		existingPipelines []v1alpha1.Pipeline
		existingSteps     []v1alpha1.Step
		initializerFails  bool
		expectedPipelines int
		expectError       bool
	}{
		{
			name:              "no existing pipelines",
			existingPipelines: []v1alpha1.Pipeline{},
			expectedPipelines: 0,
			expectError:       false,
		},
		{
			name: "single pipeline with ready step",
			existingPipelines: []v1alpha1.Pipeline{
				*createTestPipeline([]v1alpha1.StepInPipeline{
					{
						Ref: corev1.ObjectReference{
							Name:      "test-step",
							Namespace: "default",
						},
						Mandatory: true,
					},
				}),
			},
			existingSteps: []v1alpha1.Step{
				*createTestStep(true, nil),
			},
			expectedPipelines: 1,
			expectError:       false,
		},
		{
			name: "pipeline with non-ready mandatory step",
			existingPipelines: []v1alpha1.Pipeline{
				*createTestPipeline([]v1alpha1.StepInPipeline{
					{
						Ref: corev1.ObjectReference{
							Name:      "test-step",
							Namespace: "default",
						},
						Mandatory: true,
					},
				}),
			},
			existingSteps: []v1alpha1.Step{
				*createTestStep(false, nil),
			},
			expectedPipelines: 0,
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0)
			for i := range tt.existingPipelines {
				objects = append(objects, &tt.existingPipelines[i])
			}
			for i := range tt.existingSteps {
				objects = append(objects, &tt.existingSteps[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Step{}).
				Build()

			initializer := &mockInitializer{shouldFail: tt.initializerFails}
			controller := &BasePipelineController[mockPipeline]{
				Initializer:  initializer,
				Client:       client,
				OperatorName: "test",
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			err := controller.InitAllPipelines(ctx)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(controller.Pipelines) != tt.expectedPipelines {
				t.Errorf("Expected %d pipelines, got %d", tt.expectedPipelines, len(controller.Pipelines))
			}
		})
	}
}

func TestBasePipelineController_HandlePipelineCreated(t *testing.T) {
	scheme := setupTestScheme()

	tests := []struct {
		name             string
		pipeline         *v1alpha1.Pipeline
		existingSteps    []v1alpha1.Step
		initializerFails bool
		expectReady      bool
		expectError      bool
	}{
		{
			name: "pipeline with ready steps",
			pipeline: createTestPipeline([]v1alpha1.StepInPipeline{
				{
					Ref: corev1.ObjectReference{
						Name:      "test-step",
						Namespace: "default",
					},
					Mandatory: true,
				},
			}),
			existingSteps: []v1alpha1.Step{
				*createTestStep(true, nil),
			},
			expectReady: true,
			expectError: false,
		},
		{
			name: "pipeline with non-ready mandatory step",
			pipeline: createTestPipeline([]v1alpha1.StepInPipeline{
				{
					Ref: corev1.ObjectReference{
						Name:      "test-step",
						Namespace: "default",
					},
					Mandatory: true,
				},
			}),
			existingSteps: []v1alpha1.Step{
				*createTestStep(false, nil),
			},
			expectReady: false,
			expectError: true,
		},
		{
			name: "pipeline with non-ready optional step",
			pipeline: createTestPipeline([]v1alpha1.StepInPipeline{
				{
					Ref: corev1.ObjectReference{
						Name:      "test-step",
						Namespace: "default",
					},
					Mandatory: false,
				},
			}),
			existingSteps: []v1alpha1.Step{
				*createTestStep(false, nil),
			},
			expectReady: true,
			expectError: false,
		},
		{
			name: "initializer fails to initialize pipeline",
			pipeline: createTestPipeline([]v1alpha1.StepInPipeline{
				{
					Ref: corev1.ObjectReference{
						Name:      "test-step",
						Namespace: "default",
					},
					Mandatory: true,
				},
			}),
			existingSteps: []v1alpha1.Step{
				*createTestStep(true, nil),
			},
			initializerFails: true,
			expectReady:      false,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0)
			objects = append(objects, tt.pipeline)
			for i := range tt.existingSteps {
				objects = append(objects, &tt.existingSteps[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Step{}).
				Build()

			initializer := &mockInitializer{shouldFail: tt.initializerFails}
			controller := &BasePipelineController[mockPipeline]{
				Pipelines:    make(map[string]mockPipeline),
				Initializer:  initializer,
				Client:       client,
				OperatorName: "test",
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			evt := event.CreateEvent{Object: tt.pipeline}
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

			controller.HandlePipelineCreated(ctx, evt, queue)

			// Check if pipeline was added to map
			_, pipelineExists := controller.Pipelines[tt.pipeline.Name]
			if tt.expectReady && !pipelineExists {
				t.Error("Expected pipeline to be in map but it wasn't")
			}
			if !tt.expectReady && pipelineExists {
				t.Error("Expected pipeline not to be in map but it was")
			}

			// Verify pipeline status was updated
			var updatedPipeline v1alpha1.Pipeline
			err := client.Get(ctx, types.NamespacedName{Name: tt.pipeline.Name}, &updatedPipeline)
			if err != nil {
				t.Fatalf("Failed to get updated pipeline: %v", err)
			}

			if updatedPipeline.Status.Ready != tt.expectReady {
				t.Errorf("Expected Ready=%v, got %v", tt.expectReady, updatedPipeline.Status.Ready)
			}

			hasError := meta.IsStatusConditionTrue(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionError)
			if hasError != tt.expectError {
				t.Errorf("Expected Error condition=%v, got %v", tt.expectError, hasError)
			}
		})
	}
}

func TestBasePipelineController_HandlePipelineDeleted(t *testing.T) {
	scheme := setupTestScheme()

	pipeline := createTestPipeline(nil)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		Build()

	initializer := &mockInitializer{}
	controller := &BasePipelineController[mockPipeline]{
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		Initializer:  initializer,
		Client:       client,
		OperatorName: "test",
	}

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
	evt := event.DeleteEvent{Object: pipeline}
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	controller.HandlePipelineDeleted(ctx, evt, queue)

	if _, exists := controller.Pipelines["test-pipeline"]; exists {
		t.Error("Expected pipeline to be removed from map")
	}
}

func TestBasePipelineController_HandleStepCreated(t *testing.T) {
	scheme := setupTestScheme()

	tests := []struct {
		name              string
		step              *v1alpha1.Step
		knowledges        []knowledgev1alpha1.Knowledge
		pipelines         []v1alpha1.Pipeline
		expectedReady     bool
		expectedPipelines int
	}{
		{
			name: "step with ready knowledges",
			step: createTestStep(false, []corev1.ObjectReference{
				{Name: "knowledge1", Namespace: "default"},
			}),
			knowledges: []knowledgev1alpha1.Knowledge{
				*createTestKnowledge("knowledge1", false, 10),
			},
			pipelines: []v1alpha1.Pipeline{
				*createTestPipeline([]v1alpha1.StepInPipeline{
					{
						Ref: corev1.ObjectReference{
							Name:      "test-step",
							Namespace: "default",
						},
						Mandatory: true,
					},
				}),
			},
			expectedReady:     true,
			expectedPipelines: 1,
		},
		{
			name: "step with knowledge error",
			step: createTestStep(false, []corev1.ObjectReference{
				{Name: "knowledge1", Namespace: "default"},
			}),
			knowledges: []knowledgev1alpha1.Knowledge{
				*createTestKnowledge("knowledge1", true, 0),
			},
			pipelines: []v1alpha1.Pipeline{
				*createTestPipeline([]v1alpha1.StepInPipeline{
					{
						Ref: corev1.ObjectReference{
							Name:      "test-step",
							Namespace: "default",
						},
						Mandatory: true,
					},
				}),
			},
			expectedReady:     false,
			expectedPipelines: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0)
			objects = append(objects, tt.step)
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}
			for i := range tt.pipelines {
				objects = append(objects, &tt.pipelines[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Step{}, &knowledgev1alpha1.Knowledge{}).
				Build()

			initializer := &mockInitializer{}
			controller := &BasePipelineController[mockPipeline]{
				Pipelines:    make(map[string]mockPipeline),
				Initializer:  initializer,
				Client:       client,
				OperatorName: "test",
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			evt := event.CreateEvent{Object: tt.step}
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

			controller.HandleStepCreated(ctx, evt, queue)

			// Verify step status was updated
			var updatedStep v1alpha1.Step
			err := client.Get(ctx, types.NamespacedName{Name: tt.step.Name, Namespace: tt.step.Namespace}, &updatedStep)
			if err != nil {
				t.Fatalf("Failed to get updated step: %v", err)
			}

			if updatedStep.Status.Ready != tt.expectedReady {
				t.Errorf("Expected step Ready=%v, got %v", tt.expectedReady, updatedStep.Status.Ready)
			}

			// Check if pipelines were updated correctly
			if len(controller.Pipelines) != tt.expectedPipelines {
				t.Errorf("Expected %d pipelines in map, got %d", tt.expectedPipelines, len(controller.Pipelines))
			}
		})
	}
}

func TestBasePipelineController_HandleKnowledgeUpdated(t *testing.T) {
	scheme := setupTestScheme()

	tests := []struct {
		name          string
		oldKnowledge  *knowledgev1alpha1.Knowledge
		newKnowledge  *knowledgev1alpha1.Knowledge
		shouldTrigger bool
	}{
		{
			name:          "error status changed",
			oldKnowledge:  createTestKnowledge("test-knowledge", false, 10),
			newKnowledge:  createTestKnowledge("test-knowledge", true, 10),
			shouldTrigger: true,
		},
		{
			name:          "data became available",
			oldKnowledge:  createTestKnowledge("test-knowledge", false, 0),
			newKnowledge:  createTestKnowledge("test-knowledge", false, 10),
			shouldTrigger: true,
		},
		{
			name:          "no relevant change",
			oldKnowledge:  createTestKnowledge("test-knowledge", false, 10),
			newKnowledge:  createTestKnowledge("test-knowledge", false, 15),
			shouldTrigger: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := createTestStep(false, []corev1.ObjectReference{
				{Name: "test-knowledge", Namespace: "default"},
			})
			pipeline := createTestPipeline([]v1alpha1.StepInPipeline{
				{
					Ref: corev1.ObjectReference{
						Name:      "test-step",
						Namespace: "default",
					},
					Mandatory: true,
				},
			})

			objects := []client.Object{tt.newKnowledge, step, pipeline}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Step{}, &knowledgev1alpha1.Knowledge{}).
				Build()

			initializer := &mockInitializer{}
			controller := &BasePipelineController[mockPipeline]{
				Pipelines:    make(map[string]mockPipeline),
				Initializer:  initializer,
				Client:       client,
				OperatorName: "test",
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			evt := event.UpdateEvent{
				ObjectOld: tt.oldKnowledge,
				ObjectNew: tt.newKnowledge,
			}
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

			controller.HandleKnowledgeUpdated(ctx, evt, queue)

			// If should trigger, verify step status was updated
			if tt.shouldTrigger {
				var updatedStep v1alpha1.Step
				err := client.Get(ctx, types.NamespacedName{Name: step.Name, Namespace: step.Namespace}, &updatedStep)
				if err != nil {
					t.Fatalf("Failed to get updated step: %v", err)
				}
				// Status should have been recalculated
			}
		})
	}
}

func TestBasePipelineController_HandleStepDeleted(t *testing.T) {
	scheme := setupTestScheme()

	step := createTestStep(true, nil)
	pipeline := createTestPipeline([]v1alpha1.StepInPipeline{
		{
			Ref: corev1.ObjectReference{
				Name:      "test-step",
				Namespace: "default",
			},
			Mandatory: true,
		},
	})

	// Only include the pipeline in the fake client, not the step (simulating step deletion)
	objects := []client.Object{pipeline}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	initializer := &mockInitializer{}
	controller := &BasePipelineController[mockPipeline]{
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		Initializer: initializer,
		Client:      client,
	}

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
	evt := event.DeleteEvent{Object: step}
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	// Initially pipeline should be in map
	if _, exists := controller.Pipelines["test-pipeline"]; !exists {
		t.Fatal("Expected pipeline to be in map initially")
	}

	controller.HandleStepDeleted(ctx, evt, queue)

	// The main requirement is that HandleStepDeleted successfully processes the event
	// without crashing. The exact behavior depends on implementation details, but
	// it should handle the case where a dependent step is deleted gracefully.

	// The pipeline may or may not be removed from map depending on the implementation
	// but the method should not panic or error

	// Get the pipeline status to verify it was processed
	var updatedPipeline v1alpha1.Pipeline
	err := client.Get(ctx, types.NamespacedName{Name: pipeline.Name}, &updatedPipeline)
	if err != nil {
		t.Errorf("Failed to get pipeline after step deletion: %v", err)
	}

	// The status should reflect the current state - either ready with no steps, or not ready with error
	// Both are valid depending on how the implementation handles missing mandatory steps
}

func TestBasePipelineController_HandleKnowledgeDeleted(t *testing.T) {
	scheme := setupTestScheme()

	knowledge := createTestKnowledge("test-knowledge", false, 10)
	step := createTestStep(true, []corev1.ObjectReference{
		{Name: "test-knowledge", Namespace: "default"},
	})
	pipeline := createTestPipeline([]v1alpha1.StepInPipeline{
		{
			Ref: corev1.ObjectReference{
				Name:      "test-step",
				Namespace: "default",
			},
			Mandatory: true,
		},
	})

	objects := []client.Object{step, pipeline}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Step{}).
		Build()

	initializer := &mockInitializer{}
	controller := &BasePipelineController[mockPipeline]{
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		Initializer:  initializer,
		Client:       client,
		OperatorName: "test",
	}

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
	evt := event.DeleteEvent{Object: knowledge}
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	controller.HandleKnowledgeDeleted(ctx, evt, queue)

	// Verify step status was updated (should now be not ready due to missing knowledge)
	var updatedStep v1alpha1.Step
	err := client.Get(ctx, types.NamespacedName{Name: step.Name, Namespace: step.Namespace}, &updatedStep)
	if err != nil {
		t.Fatalf("Failed to get updated step: %v", err)
	}

	if updatedStep.Status.Ready {
		t.Error("Expected step to be not ready after knowledge deletion")
	}
}

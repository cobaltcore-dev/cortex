// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
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
		name              string
		pipeline          *v1alpha1.Pipeline
		knowledges        []v1alpha1.Knowledge
		schedulingDomain  v1alpha1.SchedulingDomain
		initPipelineError bool
		expectReady       bool
		expectInMap       bool
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
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      true,
			expectInMap:      true,
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
			knowledges:        []v1alpha1.Knowledge{},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			initPipelineError: true,
			expectReady:       false,
			expectInMap:       false,
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
			knowledges:       []v1alpha1.Knowledge{},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      false,
			expectInMap:      false,
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
				pipelineType: v1alpha1.PipelineTypeFilterWeigher,
			}

			if tt.initPipelineError {
				initializer.initPipelineFunc = func(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[mockPipeline] {
					return PipelineInitResult[mockPipeline]{
						FilterErrors: map[string]error{
							"test-filter": errors.New("failed to init filter"),
						},
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

			controller.handlePipelineChange(context.Background(), tt.pipeline)

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
		})
	}
}

func TestBasePipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		pipeline          *v1alpha1.Pipeline
		pipelineExists    bool
		schedulingDomain  v1alpha1.SchedulingDomain
		initPipelineError bool
		expectInMap       bool
		expectReady       bool
	}{
		{
			name: "reconcile new pipeline",
			pipeline: &v1alpha1.Pipeline{
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
			pipelineExists:   true,
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectInMap:      true,
			expectReady:      true,
		},
		{
			name: "reconcile updated pipeline",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Description:      "Updated description",
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineExists:   true,
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectInMap:      true,
			expectReady:      true,
		},
		{
			name: "reconcile deleted pipeline",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "deleted-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
				},
			},
			pipelineExists:   false,
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectInMap:      false,
			expectReady:      false,
		},
		{
			name: "reconcile pipeline with different scheduling domain",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cinder-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineExists:   true,
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectInMap:      false,
			expectReady:      false,
		},
		{
			name: "reconcile pipeline with init error",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "error-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineExists:    true,
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			initPipelineError: true,
			expectInMap:       false,
			expectReady:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.pipelineExists {
				objects = append(objects, tt.pipeline)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}).
				Build()

			initializer := &mockPipelineInitializer{
				pipelineType: v1alpha1.PipelineTypeFilterWeigher,
			}
			if tt.initPipelineError {
				initializer.initPipelineFunc = func(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[mockPipeline] {
					return PipelineInitResult[mockPipeline]{
						FilterErrors: map[string]error{
							"test-filter": errors.New("filter initialization failed"),
						},
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

			// For delete test, pre-populate the maps
			if !tt.pipelineExists {
				controller.Pipelines[tt.pipeline.Name] = mockPipeline{name: tt.pipeline.Name}
				controller.PipelineConfigs[tt.pipeline.Name] = *tt.pipeline
			}

			req := ctrl.Request{
				NamespacedName: client.ObjectKey{Name: tt.pipeline.Name},
			}

			_, err := controller.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile failed: %v", err)
			}

			// Check if pipeline is in map
			_, inMap := controller.Pipelines[tt.pipeline.Name]
			if inMap != tt.expectInMap {
				t.Errorf("Expected pipeline in map: %v, got: %v", tt.expectInMap, inMap)
			}

			// Check pipeline status if it exists
			if tt.pipelineExists {
				var updatedPipeline v1alpha1.Pipeline
				err := fakeClient.Get(context.Background(), client.ObjectKey{Name: tt.pipeline.Name}, &updatedPipeline)
				if err != nil {
					t.Fatalf("Failed to get updated pipeline: %v", err)
				}

				ready := meta.IsStatusConditionTrue(updatedPipeline.Status.Conditions, v1alpha1.PipelineConditionReady)
				if ready != tt.expectReady {
					t.Errorf("Expected ready: %v, got: %v", tt.expectReady, ready)
				}
			}
		})
	}
}

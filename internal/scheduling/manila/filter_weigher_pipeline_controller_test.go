// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/sapcc/go-bits/must"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterWeigherPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	manilaRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"share_id": "test-share-id",
			"size":     10,
		},
		Context: api.ManilaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: "global-req-123",
		},
		Hosts: []api.ExternalSchedulerHost{
			{ShareHost: "manila-share-1@backend1"},
			{ShareHost: "manila-share-2@backend2"},
		},
		Weights:  map[string]float64{"manila-share-1@backend1": 1.0, "manila-share-2@backend2": 0.5},
		Pipeline: "test-pipeline",
		Options:  scheduling.Options{SkipHistory: true},
	}

	manilaRaw, err := json.Marshal(manilaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal manila request: %v", err)
	}

	tests := []struct {
		name         string
		decision     *v1alpha1.Decision
		pipeline     *v1alpha1.Pipeline
		expectError  bool
		expectResult bool
	}{
		{
			name: "successful manila decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
		{
			name: "decision without manilaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
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
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipeline:     nil,
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
					Client:    client,
					Pipelines: make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
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
				if err != nil {
					t.Fatalf("Failed to init pipeline: %v", err)
				}
				controller.Pipelines[tt.pipeline.Name] = initResult.Pipeline
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
				t.Fatalf("Failed to get updated decision: %v", err)
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

func TestFilterWeigherPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	manilaRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"share_id": "test-share-id",
			"size":     10,
		},
		Context: api.ManilaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: "global-req-123",
		},
		Hosts: []api.ExternalSchedulerHost{
			{ShareHost: "manila-share-1@backend1"},
			{ShareHost: "manila-share-2@backend2"},
		},
		Weights:  map[string]float64{"manila-share-1@backend1": 1.0, "manila-share-2@backend2": 0.5},
		Pipeline: "test-pipeline",
	}

	manilaRaw, err := json.Marshal(manilaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal manila request: %v", err)
	}

	tests := []struct {
		name                 string
		decision             *v1alpha1.Decision
		pipelineConfig       *v1alpha1.Pipeline
		createHistory        bool
		expectError          bool
		expectHistoryCreated bool
		expectResult         bool
	}{
		{
			name: "successful decision processing with creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-decision-",
					Namespace:    "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					ResourceID:       "test-uuid-1",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        true,
			expectError:          false,
			expectHistoryCreated: true,
			expectResult:         true,
		},
		{
			name: "successful decision processing without creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-create",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        false,
			expectError:          false,
			expectHistoryCreated: false,
			expectResult:         true,
		},
		{
			name: "pipeline not configured",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig:       nil,
			expectError:          true,
			expectHistoryCreated: false,
			expectResult:         false,
		},
		{
			name: "decision without manilaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: nil,
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        true,
			expectError:          true,
			expectHistoryCreated: false,
			expectResult:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if tt.pipelineConfig != nil {
				objects = append(objects, tt.pipelineConfig)
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

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				initResult := controller.InitPipeline(t.Context(), *tt.pipelineConfig)
				if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
					t.Fatalf("Failed to init pipeline: %v", initResult)
				}
				controller.Pipelines[tt.pipelineConfig.Name] = initResult.Pipeline
			}

			if tt.decision.Spec.ManilaRaw != nil {
				req := manilaRequest
				req.Options = scheduling.Options{SkipHistory: !tt.createHistory}
				raw, marshalErr := json.Marshal(req)
				if marshalErr != nil {
					t.Fatalf("Failed to marshal request with options: %v", marshalErr)
				}
				tt.decision.Spec.ManilaRaw = &runtime.RawExtension{Raw: raw}
			}

			err := controller.ProcessNewDecisionFromAPI(context.Background(), tt.decision)
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
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
			} else if !tt.expectError {
				if tt.expectResult && tt.decision.Status.Result == nil {
					t.Error("expected decision result to be set in original decision object")
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
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name                   string
		filters                []v1alpha1.FilterSpec
		weighers               []v1alpha1.WeigherSpec
		knowledges             []client.Object
		expectNonCriticalError bool
		expectCriticalError    bool
		expectUnknownFilter    bool
		expectUnknownWeigher   bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			knowledges:             []client.Object{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "supported netapp step",
			weighers: []v1alpha1.WeigherSpec{
				{
					Name: "netapp_cpu_usage_balancing",
					Params: []v1alpha1.Parameter{
						{Key: "AvgCPUUsageLowerBound", FloatValue: new(0.0)},
						{Key: "AvgCPUUsageUpperBound", FloatValue: new(90.0)},
						{Key: "MaxCPUUsageLowerBound", FloatValue: new(0.0)},
						{Key: "MaxCPUUsageUpperBound", FloatValue: new(100.0)},
					},
				},
			},
			knowledges: []client.Object{
				&v1alpha1.Knowledge{
					ObjectMeta: metav1.ObjectMeta{
						Name: "netapp-storage-pool-cpu-usage-manila",
					},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionReady,
								Status: metav1.ConditionTrue,
							},
						},
						Raw: must.Return(v1alpha1.BoxFeatureList([]storage.StoragePoolCPUUsage{
							{
								StoragePoolName: "manila-share-1@backend1",
								AvgCPUUsagePct:  50,
								MaxCPUUsagePct:  80,
							},
							{
								StoragePoolName: "manila-share-2@backend2",
								AvgCPUUsagePct:  20,
								MaxCPUUsagePct:  40,
							},
						})),
						RawLength: 2,
					},
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
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.knowledges...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()
			controller := &FilterWeigherPipelineController{
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}
			controller.Client = client // Through basepipelinecontroller

			initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					Filters:          tt.filters,
					Weighers:         tt.weighers,
				},
			})

			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("Expected no critical error but got: %v", initResult.FilterErrors)
			}
			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("Expected critical error but got none")
			}

			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("Expected no non-critical error but got: %v", initResult.WeigherErrors)
			}
			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("Expected non-critical error but got none")
			}
		})
	}
}

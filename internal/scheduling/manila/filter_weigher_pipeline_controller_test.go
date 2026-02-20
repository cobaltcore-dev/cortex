// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/sapcc/go-bits/must"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
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
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
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
					"share_id": "test-share-id",
				},
				Context: api.ManilaRequestContext{
					RequestID: "req-123",
				},
				Hosts:    []api.ExternalSchedulerHost{{ShareHost: "manila-share-1@backend1"}},
				Weights:  map[string]float64{"manila-share-1@backend1": 1.0},
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
					"share_id": "test-share-id",
				},
				Context: api.ManilaRequestContext{
					RequestID: "req-123",
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
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
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
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			knowledges:             []client.Object{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "supported netapp step",
			weighers: []v1alpha1.WeigherSpec{
				{
					Name: "netapp_cpu_usage_balancing",
					Params: runtime.RawExtension{
						Raw: []byte(`{"AvgCPUUsageLowerBound": 0, "AvgCPUUsageUpperBound": 90, "MaxCPUUsageLowerBound": 0, "MaxCPUUsageUpperBound": 100}`),
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

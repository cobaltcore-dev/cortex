// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPipelineStateKPI_Init(t *testing.T) {
	kpi := &PipelineStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"pipelineSchedulingDomain": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPipelineStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		pipelines     []v1alpha1.Pipeline
		operator      string
		expectedCount int
		description   string
	}{
		{
			name:          "no pipelines",
			pipelines:     []v1alpha1.Pipeline{},
			operator:      "test-operator",
			expectedCount: 0,
			description:   "should not collect metrics when no pipelines exist",
		},
		{
			name: "single ready pipeline",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline1"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for ready pipeline",
		},
		{
			name: "pipeline in error state",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline2"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready: false,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.PipelineConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for error pipeline",
		},
		{
			name: "multiple pipelines different states",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-ready"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-error"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready: false,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.PipelineConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for all pipelines with different states",
		},
		{
			name: "filter by operator",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-correct-operator"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-wrong-operator"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "other-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should only collect metrics for pipelines with matching operator",
		},
		{
			name: "pipeline with unknown state",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-unknown"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready:      false,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric with unknown state for pipeline without ready status or error condition",
		},
		{
			name: "error condition takes precedence over ready status",
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: v1.ObjectMeta{Name: "pipeline-error-priority"},
					Spec:       v1alpha1.PipelineSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.PipelineStatus{
						Ready: true,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.PipelineConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should report error state even if ready status is true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.Pipeline, len(tt.pipelines))
			copy(objects, tt.pipelines)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &PipelineStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"pipelineSchedulingDomain": "`+tt.operator+`"}`)); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			ch := make(chan prometheus.Metric, 10)
			kpi.Collect(ch)
			close(ch)

			metricsCount := 0
			for range ch {
				metricsCount++
			}

			if metricsCount != tt.expectedCount {
				t.Errorf("%s: expected %d metrics, got %d", tt.description, tt.expectedCount, metricsCount)
			}
		})
	}
}

func TestPipelineStateKPI_GetName(t *testing.T) {
	kpi := &PipelineStateKPI{}
	expectedName := "pipeline_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestPipelineStateKPI_Describe(t *testing.T) {
	kpi := &PipelineStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"pipelineSchedulingDomain": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan *prometheus.Desc, 1)
	kpi.Describe(ch)
	close(ch)

	descCount := 0
	for range ch {
		descCount++
	}

	if descCount != 1 {
		t.Errorf("expected 1 descriptor, got %d", descCount)
	}
}

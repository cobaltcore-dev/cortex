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

func TestKnowledgeStateKPI_Init(t *testing.T) {
	kpi := &KnowledgeStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"knowledgeSchedulingDomain": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestKnowledgeStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		knowledges    []v1alpha1.Knowledge
		operator      string
		expectedCount int
		description   string
	}{
		{
			name:          "no knowledges",
			knowledges:    []v1alpha1.Knowledge{},
			operator:      "test-operator",
			expectedCount: 0,
			description:   "should not collect metrics when no knowledges exist",
		},
		{
			name: "single ready knowledge",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn1"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						RawLength:  10,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for ready knowledge",
		},
		{
			name: "knowledge in error state",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn2"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for error knowledge",
		},
		{
			name: "multiple knowledges different states",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn-ready"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						RawLength:  10,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn-error"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for all knowledges with different states",
		},
		{
			name: "filter by operator",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn-correct-operator"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						RawLength:  10,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn-wrong-operator"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "other-operator"},
					Status: v1alpha1.KnowledgeStatus{
						RawLength:  10,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should only collect metrics for knowledges with matching operator",
		},
		{
			name: "knowledge with unknown state",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn-unknown"},
					Spec:       v1alpha1.KnowledgeSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KnowledgeStatus{
						RawLength:  0,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric with unknown state for knowledge without objects or conditions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.Knowledge, len(tt.knowledges))
			copy(objects, tt.knowledges)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &KnowledgeStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"knowledgeSchedulingDomain": "`+tt.operator+`"}`)); err != nil {
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

func TestKnowledgeStateKPI_GetName(t *testing.T) {
	kpi := &KnowledgeStateKPI{}
	expectedName := "knowledge_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestKnowledgeStateKPI_Describe(t *testing.T) {
	kpi := &KnowledgeStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"knowledgeSchedulingDomain": "test-operator"}`)); err != nil {
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

// Copyright 2025 SAP SE
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

func TestStepStateKPI_Init(t *testing.T) {
	kpi := &StepStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"stepOperator": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestStepStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		steps         []v1alpha1.Step
		operator      string
		expectedCount int
		description   string
	}{
		{
			name:          "no steps",
			steps:         []v1alpha1.Step{},
			operator:      "test-operator",
			expectedCount: 0,
			description:   "should not collect metrics when no steps exist",
		},
		{
			name: "single ready step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step1"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for ready step",
		},
		{
			name: "step in error state",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step2"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready: false,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.StepConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for error step",
		},
		{
			name: "multiple steps different states",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-ready"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-error"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready: false,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.StepConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for all steps with different states",
		},
		{
			name: "filter by operator",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-correct-operator"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-wrong-operator"},
					Spec:       v1alpha1.StepSpec{Operator: "other-operator"},
					Status: v1alpha1.StepStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should only collect metrics for steps with matching operator",
		},
		{
			name: "step with unknown state",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-unknown"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready:      false,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric with unknown state for step without ready status or error condition",
		},
		{
			name: "error condition takes precedence over ready status",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: v1.ObjectMeta{Name: "step-error-priority"},
					Spec:       v1alpha1.StepSpec{Operator: "test-operator"},
					Status: v1alpha1.StepStatus{
						Ready: true,
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.StepConditionError,
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
			objects := make([]v1alpha1.Step, len(tt.steps))
			copy(objects, tt.steps)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &StepStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"stepOperator": "`+tt.operator+`"}`)); err != nil {
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

func TestStepStateKPI_GetName(t *testing.T) {
	kpi := &StepStateKPI{}
	expectedName := "step_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestStepStateKPI_Describe(t *testing.T) {
	kpi := &StepStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"stepOperator": "test-operator"}`)); err != nil {
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

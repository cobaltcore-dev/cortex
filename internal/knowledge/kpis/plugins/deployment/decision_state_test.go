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

func TestDecisionStateKPI_Init(t *testing.T) {
	kpi := &DecisionStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"decisionOperator": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDecisionStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	targetHost := "host1"

	tests := []struct {
		name            string
		decisions       []v1alpha1.Decision
		operator        string
		expectedCount   int
		description     string
		expectedError   int
		expectedWaiting int
		expectedSuccess int
	}{
		{
			name:            "no decisions",
			decisions:       []v1alpha1.Decision{},
			operator:        "test-operator",
			expectedCount:   3, // always emits 3 metrics: error, waiting, success
			description:     "should collect metrics with zero counts when no decisions exist",
			expectedError:   0,
			expectedWaiting: 0,
			expectedSuccess: 0,
		},
		{
			name: "single decision in error state",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec1"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should count decision in error state",
			expectedError:   1,
			expectedWaiting: 0,
			expectedSuccess: 0,
		},
		{
			name: "single decision in waiting state",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec2"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: &targetHost,
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should count decision with target host as waiting",
			expectedError:   0,
			expectedWaiting: 1,
			expectedSuccess: 0,
		},
		{
			name: "single decision in success state",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec3"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							OrderedHosts: []string{"host1", "host2"},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should count decision without target host and no error as success",
			expectedError:   0,
			expectedWaiting: 0,
			expectedSuccess: 1,
		},
		{
			name: "multiple decisions in different states",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-error"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-waiting"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: &targetHost,
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-success"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							OrderedHosts: []string{"host1"},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should correctly count decisions across all states",
			expectedError:   1,
			expectedWaiting: 1,
			expectedSuccess: 1,
		},
		{
			name: "filter by operator",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-correct-operator"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-wrong-operator"},
					Spec:       v1alpha1.DecisionSpec{Operator: "other-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should only count decisions with matching operator",
			expectedError:   1,
			expectedWaiting: 0,
			expectedSuccess: 0,
		},
		{
			name: "multiple decisions same state",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-error-1"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-error-2"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should correctly aggregate multiple decisions in same state",
			expectedError:   2,
			expectedWaiting: 0,
			expectedSuccess: 0,
		},
		{
			name: "decision with no result",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-no-result"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status:     v1alpha1.DecisionStatus{},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should count decision with no result as success",
			expectedError:   0,
			expectedWaiting: 0,
			expectedSuccess: 1,
		},
		{
			name: "error condition takes precedence",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: v1.ObjectMeta{Name: "dec-error-with-target"},
					Spec:       v1alpha1.DecisionSpec{Operator: "test-operator"},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: &targetHost,
						},
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DecisionConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:        "test-operator",
			expectedCount:   3,
			description:     "should count as error even if target host is present",
			expectedError:   1,
			expectedWaiting: 0,
			expectedSuccess: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.Decision, len(tt.decisions))
			copy(objects, tt.decisions)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &DecisionStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"decisionOperator": "`+tt.operator+`"}`)); err != nil {
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

func TestDecisionStateKPI_GetName(t *testing.T) {
	kpi := &DecisionStateKPI{}
	expectedName := "decision_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestDecisionStateKPI_Describe(t *testing.T) {
	kpi := &DecisionStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"decisionOperator": "test-operator"}`)); err != nil {
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

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

func TestDatasourceStateKPI_Init(t *testing.T) {
	kpi := &DatasourceStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"datasourceOperator": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDatasourceStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		datasources   []v1alpha1.Datasource
		operator      string
		expectedCount int
		description   string
	}{
		{
			name:          "no datasources",
			datasources:   []v1alpha1.Datasource{},
			operator:      "test-operator",
			expectedCount: 0,
			description:   "should not collect metrics when no datasources exist",
		},
		{
			name: "single ready datasource",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds1"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for ready datasource",
		},
		{
			name: "datasource in waiting state",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds2"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionWaiting,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for waiting datasource",
		},
		{
			name: "datasource in error state",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds3"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for error datasource",
		},
		{
			name: "multiple datasources different states",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-ready"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-waiting"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionWaiting,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-error"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 3,
			description:   "should collect metrics for all datasources with different states",
		},
		{
			name: "filter by operator",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-correct-operator"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-wrong-operator"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "other-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should only collect metrics for datasources with matching operator",
		},
		{
			name: "datasource with unknown state",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-unknown"},
					Spec:       v1alpha1.DatasourceSpec{Operator: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 0,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric with unknown state for datasource without objects or conditions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.Datasource, len(tt.datasources))
			copy(objects, tt.datasources)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &DatasourceStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"datasourceOperator": "`+tt.operator+`"}`)); err != nil {
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

func TestDatasourceStateKPI_GetName(t *testing.T) {
	kpi := &DatasourceStateKPI{}
	expectedName := "datasource_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestDatasourceStateKPI_Describe(t *testing.T) {
	kpi := &DatasourceStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"datasourceOperator": "test-operator"}`)); err != nil {
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

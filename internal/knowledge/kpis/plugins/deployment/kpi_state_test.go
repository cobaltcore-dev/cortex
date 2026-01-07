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

func TestKPIStateKPI_Init(t *testing.T) {
	kpi := &KPIStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"kpiSchedulingDomain": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestKPIStateKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		kpis          []v1alpha1.KPI
		operator      string
		expectedCount int
		description   string
	}{
		{
			name:          "no kpis",
			kpis:          []v1alpha1.KPI{},
			operator:      "test-operator",
			expectedCount: 0,
			description:   "should not collect metrics when no kpis exist",
		},
		{
			name: "single ready kpi",
			kpis: []v1alpha1.KPI{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn1"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for ready kpi",
		},
		{
			name: "kpi in error state",
			kpis: []v1alpha1.KPI{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kn2"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.KPIConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric for error kpi",
		},
		{
			name: "multiple kpis different states",
			kpis: []v1alpha1.KPI{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kpi-ready"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "kpi-error"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.KPIConditionError,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for all kpis with different states",
		},
		{
			name: "filter by operator",
			kpis: []v1alpha1.KPI{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kpi-correct-operator"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "kpi-wrong-operator"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "other-operator"},
					Status: v1alpha1.KPIStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should only collect metrics for kpis with matching operator",
		},
		{
			name: "kpi with unknown state",
			kpis: []v1alpha1.KPI{
				{
					ObjectMeta: v1.ObjectMeta{Name: "kpi-unknown"},
					Spec:       v1alpha1.KPISpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.KPIStatus{
						Ready:      true,
						Conditions: []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 1,
			description:   "should collect metric with unknown state for kpi without objects or conditions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]v1alpha1.KPI, len(tt.kpis))
			copy(objects, tt.kpis)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range objects {
				clientBuilder = clientBuilder.WithObjects(&objects[i])
			}
			client := clientBuilder.Build()

			kpi := &KPIStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"kpiSchedulingDomain": "`+tt.operator+`"}`)); err != nil {
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

func TestKPIStateKPI_GetName(t *testing.T) {
	kpi := &KPIStateKPI{}
	expectedName := "kpi_state_kpi"
	if name := kpi.GetName(); name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, name)
	}
}

func TestKPIStateKPI_Describe(t *testing.T) {
	kpi := &KPIStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"kpiSchedulingDomain": "test-operator"}`)); err != nil {
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

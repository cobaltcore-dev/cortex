// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDatasourceStateKPI_Init(t *testing.T) {
	kpi := &DatasourceStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"datasourceSchedulingDomain": "test-operator"}`)); err != nil {
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
		expectedCount int // 2 metrics per datasource: state counter + seconds until reconcile gauge
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
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for ready datasource",
		},
		{
			name: "datasource in error state",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds3"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:    v1alpha1.DatasourceConditionReady,
								Status:  v1.ConditionFalse,
								Reason:  "SomeError",
								Message: "An error occurred",
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics for error datasource",
		},
		{
			name: "multiple datasources different states",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-ready"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-waiting"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionReady,
								Status: v1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-error"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						Conditions: []v1.Condition{
							{
								Type:   v1alpha1.DatasourceConditionReady,
								Status: v1.ConditionFalse,
							},
						},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 6,
			description:   "should collect metrics for all datasources with different states",
		},
		{
			name: "filter by operator",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-correct-operator"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-wrong-operator"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "other-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 10,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should only collect metrics for datasources with matching operator",
		},
		{
			name: "datasource with unknown state",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: v1.ObjectMeta{Name: "ds-unknown"},
					Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 0,
						Conditions:      []v1.Condition{},
					},
				},
			},
			operator:      "test-operator",
			expectedCount: 2,
			description:   "should collect metrics with unknown state for datasource without objects or conditions",
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
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"datasourceSchedulingDomain": "`+tt.operator+`"}`)); err != nil {
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
	if err := kpi.Init(nil, nil, conf.NewRawOpts(`{"datasourceSchedulingDomain": "test-operator"}`)); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan *prometheus.Desc, 2)
	kpi.Describe(ch)
	close(ch)

	descCount := 0
	for range ch {
		descCount++
	}

	if descCount != 2 {
		t.Errorf("expected 2 descriptors, got %d", descCount)
	}
}

func TestDatasourceStateKPI_GaugeSecondsUntilReconcile(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	now := time.Now()
	tests := []struct {
		name         string
		datasource   v1alpha1.Datasource
		expectQueued bool
		expectSign   int // 1 for positive, -1 for negative
	}{
		{
			name: "datasource with NextSyncTime in future",
			datasource: v1alpha1.Datasource{
				ObjectMeta: v1.ObjectMeta{Name: "ds-queued", CreationTimestamp: v1.NewTime(now.Add(-time.Hour))},
				Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
				Status: v1alpha1.DatasourceStatus{
					NextSyncTime: v1.NewTime(now.Add(30 * time.Second)),
				},
			},
			expectQueued: true,
			expectSign:   1,
		},
		{
			name: "datasource with NextSyncTime in past",
			datasource: v1alpha1.Datasource{
				ObjectMeta: v1.ObjectMeta{Name: "ds-overdue", CreationTimestamp: v1.NewTime(now.Add(-time.Hour))},
				Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
				Status: v1alpha1.DatasourceStatus{
					NextSyncTime: v1.NewTime(now.Add(-30 * time.Second)),
				},
			},
			expectQueued: true,
			expectSign:   -1,
		},
		{
			name: "datasource never reconciled (no NextSyncTime)",
			datasource: v1alpha1.Datasource{
				ObjectMeta: v1.ObjectMeta{Name: "ds-never-reconciled", CreationTimestamp: v1.NewTime(now.Add(-time.Minute))},
				Spec:       v1alpha1.DatasourceSpec{SchedulingDomain: "test-operator"},
				Status:     v1alpha1.DatasourceStatus{},
			},
			expectQueued: false,
			expectSign:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&tt.datasource).
				Build()

			kpi := &DatasourceStateKPI{}
			if err := kpi.Init(nil, client, conf.NewRawOpts(`{"datasourceSchedulingDomain": "test-operator"}`)); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			ch := make(chan prometheus.Metric, 10)
			kpi.Collect(ch)
			close(ch)

			var gaugeMetric prometheus.Metric
			for m := range ch {
				var metric dto.Metric
				if err := m.Write(&metric); err != nil {
					t.Fatalf("failed to write metric: %v", err)
				}
				for _, label := range metric.Label {
					if label.GetName() == "queued" {
						gaugeMetric = m
						expectedQueued := "false"
						if tt.expectQueued {
							expectedQueued = "true"
						}
						if label.GetValue() != expectedQueued {
							t.Errorf("expected queued=%s, got queued=%s", expectedQueued, label.GetValue())
						}
						break
					}
				}
			}

			if gaugeMetric == nil {
				t.Fatal("expected gaugeSecondsUntilReconcile metric to be collected")
			}

			var metric dto.Metric
			if err := gaugeMetric.Write(&metric); err != nil {
				t.Fatalf("failed to write metric: %v", err)
			}

			value := metric.Gauge.GetValue()
			if tt.expectSign == 1 && value <= 0 {
				t.Errorf("expected positive value, got %f", value)
			}
			if tt.expectSign == -1 && value >= 0 {
				t.Errorf("expected negative value, got %f", value)
			}
		})
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type mockKPI struct {
	name      string
	initError error
	inputs    plugins.Inputs
}

func (m *mockKPI) Init(inputs plugins.Inputs, _ client.Client, _ conf.RawOpts) error {
	m.inputs = inputs
	return m.initError
}
func (m *mockKPI) Collect(chan<- prometheus.Metric) {}
func (m *mockKPI) Describe(chan<- *prometheus.Desc) {}
func (m *mockKPI) GetName() string                  { return m.name }

func readyCondition(ready bool) []metav1.Condition {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return []metav1.Condition{{Type: "Ready", Status: status, Reason: "Test"}}
}

func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&v1alpha1.KPI{}).Build()
}

func TestController_handleKPIChange(t *testing.T) {
	inputs := []v1alpha1.InputSpec{{Var: "objs", Datasource: "ds"}, {Var: "feats", Knowledge: "k"}}
	tests := []struct {
		name            string
		inputs          []v1alpha1.InputSpec
		datasourceReady bool
		knowledgeReady  bool
		initError       error
		wantErr         bool
		wantRegistered  bool
		wantReadyFrac   string
		wantReason      string
	}{
		{"no inputs", nil, false, false, nil, false, true, "ready", "AllDependenciesReady"},
		{"all inputs ready", inputs, true, true, nil, false, true, "2/2", "AllDependenciesReady"},
		{"knowledge not ready", inputs, true, false, nil, false, false, "1/2", "UnreadyDependencies"},
		{"init fails", inputs, true, true, errors.New("init failed"), true, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kpi := &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{Name: "kpi-" + t.Name()},
				Spec:       v1alpha1.KPISpec{Impl: "test_kpi", Inputs: tt.inputs},
			}
			c := newTestClient(t,
				&v1alpha1.Datasource{
					ObjectMeta: metav1.ObjectMeta{Name: "ds"},
					Status:     v1alpha1.DatasourceStatus{Conditions: readyCondition(tt.datasourceReady)},
				},
				&v1alpha1.Knowledge{
					ObjectMeta: metav1.ObjectMeta{Name: "k"},
					Status:     v1alpha1.KnowledgeStatus{Conditions: readyCondition(tt.knowledgeReady)},
				},
			)
			instance := &mockKPI{name: "test_kpi_" + tt.name, initError: tt.initError}
			controller := &Controller{
				Client:                       c,
				supportedKPIs:                map[string]func() plugins.KPI{"test_kpi": func() plugins.KPI { return instance }},
				registeredKPIsByResourceName: map[string]plugins.KPI{},
			}
			t.Cleanup(func() {
				if registered, ok := controller.registeredKPIsByResourceName[kpi.Name]; ok {
					metrics.Registry.Unregister(registered)
				}
			})

			err := controller.handleKPIChange(t.Context(), kpi)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if _, ok := controller.registeredKPIsByResourceName[kpi.Name]; ok != tt.wantRegistered {
				t.Errorf("expected registered %v, got %v", tt.wantRegistered, ok)
			}
			if tt.wantErr {
				return
			}
			if kpi.Status.DependenciesReadyFrac != tt.wantReadyFrac {
				t.Errorf("expected ready frac %q, got %q", tt.wantReadyFrac, kpi.Status.DependenciesReadyFrac)
			}
			if cond := meta.FindStatusCondition(kpi.Status.Conditions, v1alpha1.KPIConditionReady); cond == nil || cond.Reason != tt.wantReason {
				t.Errorf("expected reason %q, got %+v", tt.wantReason, cond)
			}
			if tt.wantRegistered && instance.inputs == nil {
				t.Error("expected kpi to be initialized with its inputs")
			}
		})
	}
}

func TestController_Reconcile_KPIDeleted(t *testing.T) {
	instance := &mockKPI{name: "deleted_kpi"}
	controller := &Controller{
		Client:                       newTestClient(t),
		registeredKPIsByResourceName: map[string]plugins.KPI{"deleted-kpi": instance},
	}
	if _, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "deleted-kpi"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.registeredKPIsByResourceName["deleted-kpi"]; ok {
		t.Error("expected deleted kpi to be unregistered")
	}
}

func TestController_InitAllKPIs(t *testing.T) {
	c := newTestClient(t,
		&v1alpha1.KPI{
			ObjectMeta: metav1.ObjectMeta{Name: "kpi-ready"},
			Spec:       v1alpha1.KPISpec{SchedulingDomain: "test", Impl: "test_kpi"},
		},
		&v1alpha1.KPI{
			ObjectMeta: metav1.ObjectMeta{Name: "kpi-missing-input"},
			Spec: v1alpha1.KPISpec{SchedulingDomain: "test", Impl: "test_kpi",
				Inputs: []v1alpha1.InputSpec{{Var: "objs", Datasource: "missing"}}},
		},
		&v1alpha1.KPI{
			ObjectMeta: metav1.ObjectMeta{Name: "kpi-other-domain"},
			Spec:       v1alpha1.KPISpec{SchedulingDomain: "other", Impl: "test_kpi"},
		},
	)
	controller := &Controller{
		Client:        c,
		Config:        ControllerConfig{SchedulingDomain: "test"},
		supportedKPIs: map[string]func() plugins.KPI{"test_kpi": func() plugins.KPI { return &mockKPI{name: "init_all_kpi"} }},
	}
	t.Cleanup(func() {
		for _, registered := range controller.registeredKPIsByResourceName {
			metrics.Registry.Unregister(registered)
		}
	})
	if err := controller.InitAllKPIs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(controller.registeredKPIsByResourceName) != 1 {
		t.Fatalf("expected only the ready kpi to be registered, got %v", controller.registeredKPIsByResourceName)
	}
	if _, ok := controller.registeredKPIsByResourceName["kpi-ready"]; !ok {
		t.Error("expected kpi-ready to be registered")
	}
}

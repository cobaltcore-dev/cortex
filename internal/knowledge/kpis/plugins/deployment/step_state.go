// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StepStateKPIOpts struct {
	// The scheduling domain to filter steps by.
	StepSchedulingDomain v1alpha1.SchedulingDomain `json:"stepSchedulingDomain"`
}

// KPI observing the state of step resources managed by cortex.
type StepStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[StepStateKPIOpts]

	// Prometheus descriptor for the step state metric.
	counter *prometheus.Desc
}

func (StepStateKPI) GetName() string { return "step_state_kpi" }

// Initialize the KPI.
func (k *StepStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_step_state",
		"State of cortex managed steps",
		[]string{"operator", "step", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *StepStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the step state metrics.
func (k *StepStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all steps with the specified step operator.
	stepList := &v1alpha1.StepList{}
	if err := k.Client.List(context.Background(), stepList); err != nil {
		return
	}
	var steps []v1alpha1.Step
	for _, step := range stepList.Items {
		if step.Spec.SchedulingDomain != k.Options.StepSchedulingDomain {
			continue
		}
		steps = append(steps, step)
	}
	// For each step, emit a metric with its state.
	for _, step := range steps {
		var state string
		switch {
		case meta.IsStatusConditionTrue(step.Status.Conditions, v1alpha1.StepConditionError):
			state = "error"
		case step.Status.Ready:
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			string(k.Options.StepSchedulingDomain), step.Name, state,
		)
	}
}

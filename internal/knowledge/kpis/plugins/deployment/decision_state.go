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

type DecisionStateKPIOpts struct {
	// The scheduling domain to filter decisions by.
	DecisionSchedulingDomain v1alpha1.SchedulingDomain `json:"decisionSchedulingDomain"`
}

// KPI observing the state of decision resources managed by cortex.
type DecisionStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[DecisionStateKPIOpts]

	// Prometheus descriptor for the decision state metric.
	counter *prometheus.Desc
}

func (DecisionStateKPI) GetName() string { return "decision_state_kpi" }

// Initialize the KPI.
func (k *DecisionStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_decision_state",
		"State of cortex managed decisions (aggregated)",
		[]string{"operator", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *DecisionStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the decision state metrics.
func (k *DecisionStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all decisions with the specified decision operator.
	decisionList := &v1alpha1.DecisionList{}
	if err := k.Client.List(context.Background(), decisionList); err != nil {
		return
	}
	var decisions []v1alpha1.Decision
	for _, d := range decisionList.Items {
		if d.Spec.SchedulingDomain != k.Options.DecisionSchedulingDomain {
			continue
		}
		decisions = append(decisions, d)
	}
	// For each decision, emit a metric with its state.
	var errorCount, waitingCount, successCount float64
	for _, d := range decisions {
		switch {
		case meta.IsStatusConditionTrue(d.Status.Conditions, v1alpha1.DecisionConditionError):
			errorCount++
		case d.Status.Result == nil || d.Status.Result.TargetHost == nil:
			waitingCount++
		default:
			successCount++
		}
	}
	ch <- prometheus.MustNewConstMetric(
		k.counter, prometheus.GaugeValue, errorCount,
		string(k.Options.DecisionSchedulingDomain), "error",
	)
	ch <- prometheus.MustNewConstMetric(
		k.counter, prometheus.GaugeValue, waitingCount,
		string(k.Options.DecisionSchedulingDomain), "waiting",
	)
	ch <- prometheus.MustNewConstMetric(
		k.counter, prometheus.GaugeValue, successCount,
		string(k.Options.DecisionSchedulingDomain), "success",
	)
}

// Copyright 2025 SAP SE
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

type KPIStateKPIOpts struct {
	// The operator to filter kpis by.
	KPIOperator string `yaml:"kpiOperator"`
}

// KPI observing the state of kpi resources managed by cortex.
type KPIStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[KPIStateKPIOpts]

	// Prometheus descriptor for the kpi state metric.
	counter *prometheus.Desc
}

func (KPIStateKPI) GetName() string { return "kpi_state_kpi" }

// Initialize the KPI.
func (k *KPIStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_kpi_state",
		"State of cortex managed kpis",
		[]string{"operator", "kpi", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *KPIStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the kpi state metrics.
func (k *KPIStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all kpis with the specified kpi operator.
	kpiList := &v1alpha1.KPIList{}
	if err := k.Client.List(context.Background(), kpiList); err != nil {
		return
	}
	var kpis []v1alpha1.KPI
	for _, kpi := range kpiList.Items {
		if kpi.Spec.Operator != k.Options.KPIOperator {
			continue
		}
		kpis = append(kpis, kpi)
	}
	// For each kpi, emit a metric with its state.
	for _, kpi := range kpis {
		var state string
		switch {
		case meta.IsStatusConditionTrue(kpi.Status.Conditions, v1alpha1.KPIConditionError):
			state = "error"
		case kpi.Status.Ready:
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			k.Options.KPIOperator, kpi.Name, state,
		)
	}
}

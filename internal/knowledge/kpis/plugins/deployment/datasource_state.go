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

type DatasourceStateKPIOpts struct {
	// The scheduling domain to filter datasources by.
	DatasourceSchedulingDomain v1alpha1.SchedulingDomain `json:"datasourceSchedulingDomain"`
}

// KPI observing the state of datasource resources managed by cortex.
type DatasourceStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[DatasourceStateKPIOpts]

	// Prometheus descriptor for the datasource state metric.
	counter *prometheus.Desc
}

func (DatasourceStateKPI) GetName() string { return "datasource_state_kpi" }

// Initialize the KPI.
func (k *DatasourceStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc(
		"cortex_datasource_state",
		"State of cortex managed datasources",
		[]string{"operator", "datasource", "state"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptor.
func (k *DatasourceStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.counter }

// Collect the datasource state metrics.
func (k *DatasourceStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all datasources with the specified datasource operator.
	datasourceList := &v1alpha1.DatasourceList{}
	if err := k.Client.List(context.Background(), datasourceList); err != nil {
		return
	}
	var datasources []v1alpha1.Datasource
	for _, ds := range datasourceList.Items {
		if ds.Spec.SchedulingDomain != k.Options.DatasourceSchedulingDomain {
			continue
		}
		datasources = append(datasources, ds)
	}
	// For each datasource, emit a metric with its state.
	for _, ds := range datasources {
		var state string
		switch {
		case meta.IsStatusConditionTrue(ds.Status.Conditions, v1alpha1.DatasourceConditionWaiting):
			state = "waiting"
		case meta.IsStatusConditionTrue(ds.Status.Conditions, v1alpha1.DatasourceConditionError):
			state = "error"
		case ds.Status.IsReady():
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			string(k.Options.DatasourceSchedulingDomain), ds.Name, state,
		)
	}
}

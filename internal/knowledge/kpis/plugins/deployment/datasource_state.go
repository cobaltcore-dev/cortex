// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var datasourceStateKPILogger = ctrl.Log.WithName("datasource-state-kpi")

// DatasourceStateKPIOpts defines the options for the DatasourceStateKPI
// which are loaded through the kpi resource.
type DatasourceStateKPIOpts struct {
	// DatasourceSchedulingDomain describes the scheduling domain to filter
	// datasources by.
	DatasourceSchedulingDomain v1alpha1.SchedulingDomain `json:"datasourceSchedulingDomain"`
}

// DatasourceStateKPI observes the state of datasource resources managed by cortex.
type DatasourceStateKPI struct {
	plugins.BaseKPI[DatasourceStateKPIOpts]

	// Counter that tracks the state of datasources, labeled by domain,
	// datasource name, and state.
	counter *prometheus.Desc

	// gaugeSecondsUntilReconcile is a prometheus gauge that tracks the seconds
	// until the datasource should be reconciled again, labeled by domain and
	// datasource name. This can help identify if there are issues with the
	// reconciliation loop or if the datasource is not being updated as expected.
	gaugeSecondsUntilReconcile *prometheus.Desc
}

// GetName returns a unique name for this kpi that is used for registration
// and configuration.
func (DatasourceStateKPI) GetName() string { return "datasource_state_kpi" }

// Init initializes the kpi, e.g. by creating the necessary Prometheus
// descriptors. The base kpi is also initialized with the provided database,
// client and options.
func (k *DatasourceStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.counter = prometheus.NewDesc("cortex_datasource_state",
		"State of cortex managed datasources",
		[]string{"domain", "datasource", "state"}, nil,
	)
	k.gaugeSecondsUntilReconcile = prometheus.NewDesc("cortex_datasource_seconds_until_reconcile",
		"Seconds until the datasource should be reconciled again. "+
			"Negative values indicate the datasource is x seconds overdue for "+
			"reconciliation.",
		[]string{"domain", "datasource", "queued"}, nil,
	)
	return nil
}

// Describe sends the descriptor of this kpi to the provided channel. This is
// used by Prometheus to know which metrics this kpi exposes.
func (k *DatasourceStateKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.counter
	ch <- k.gaugeSecondsUntilReconcile
}

// Collect collects the current state of datasources from the database and
// sends it as Prometheus metrics to the provided channel.
func (k *DatasourceStateKPI) Collect(ch chan<- prometheus.Metric) {
	// Get all datasources with the specified datasource operator.
	datasourceList := &v1alpha1.DatasourceList{}
	if err := k.Client.List(context.Background(), datasourceList); err != nil {
		datasourceStateKPILogger.Error(err, "Failed to list datasources")
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
		case meta.IsStatusConditionTrue(ds.Status.Conditions, v1alpha1.DatasourceConditionReady):
			state = "ready"
		default:
			state = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(
			k.counter, prometheus.GaugeValue, 1,
			string(k.Options.DatasourceSchedulingDomain), ds.Name, state,
		)
		if !ds.Status.NextSyncTime.IsZero() {
			// This resource is queued and we can calculate the seconds until
			// it should be reconciled again (can be negative if in the past).
			secondsUntilReconcile := time.Until(ds.Status.NextSyncTime.Time).Seconds()
			ch <- prometheus.MustNewConstMetric(
				k.gaugeSecondsUntilReconcile, prometheus.GaugeValue, secondsUntilReconcile,
				string(k.Options.DatasourceSchedulingDomain), ds.Name, "true",
			)
		} else {
			// This resource is not queued (never reconciled). In this case
			// we take the time since creation as a proxy for how long it has
			// been until the first reconciliation request.
			secondsSinceCreation := time.Since(ds.CreationTimestamp.Time).Seconds()
			ch <- prometheus.MustNewConstMetric(
				k.gaugeSecondsUntilReconcile, prometheus.GaugeValue, -secondsSinceCreation,
				string(k.Options.DatasourceSchedulingDomain), ds.Name, "false",
			)
		}
	}
	datasourceStateKPILogger.Info("Collected datasource state metrics", "count", len(datasources))
}

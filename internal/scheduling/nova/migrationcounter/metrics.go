// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package migrationcounter

import "github.com/prometheus/client_golang/prometheus"

// NewTrackedEntriesCollector returns a Prometheus collector exposing the number
// of entries currently held by the counter as cortex_evacuation_tracked_entries.
// It reads the live value via counter.Len at scrape time, so the counter itself
// has no dependency on Prometheus. Register it with the metrics registry.
func NewTrackedEntriesCollector(counter *RepeatedMigrationCounter) prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "cortex_evacuation_tracked_entries",
		Help: "Number of entries currently held by the migration counter",
	}, func() float64 {
		return float64(counter.Len())
	})
}

// NewSoftForceCounter creates the Prometheus counter for evacuation soft-force
// decisions: it is incremented once per pipeline run when a VM is flagged to use
// any failover slot during evacuation.
// Register it with the metrics registry before assigning it to the controller.
func NewSoftForceCounter() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_evacuation_soft_force_total",
		Help: "Total number of times a VM was flagged for soft-force failover use during evacuation",
	})
}

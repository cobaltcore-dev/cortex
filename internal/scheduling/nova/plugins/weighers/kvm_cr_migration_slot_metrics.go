// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// CRMigrationSlotMetrics holds Prometheus metrics for the CR migration slot weigher.
type CRMigrationSlotMetrics struct {
	// Results counts live migration requests by outcome:
	//   - slot_found:      at least one candidate has a compatible CR slot
	//   - no_slot:         source slot found but no candidate is compatible
	//   - no_source_slot:  migrating VM has no confirmed CR reservation
	Results *prometheus.CounterVec
}

func NewCRMigrationSlotMetrics() *CRMigrationSlotMetrics {
	return &CRMigrationSlotMetrics{
		Results: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_nova_weigh_cr_migration_slot_requests_total",
				Help: "Live migration requests processed by the CR migration slot weigher, " +
					"labeled by outcome (slot_found, no_slot, no_source_slot).",
			},
			[]string{"result"},
		),
	}
}

func (m *CRMigrationSlotMetrics) Describe(ch chan<- *prometheus.Desc) {
	if m == nil || m.Results == nil {
		return
	}
	m.Results.Describe(ch)
}

func (m *CRMigrationSlotMetrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil || m.Results == nil {
		return
	}
	m.Results.Collect(ch)
}

var recordCRMigrationSlotResultNilOnce = &sync.Once{}

func (m *CRMigrationSlotMetrics) RecordResult(result string) {
	if m == nil || m.Results == nil {
		recordCRMigrationSlotResultNilOnce.Do(func() {
			slog.Warn("CRMigrationSlotMetrics is nil; result metric not recorded "+
				"(is CRMigrationSlotMetricsSingleton initialized in cmd/manager?)",
				"result", result,
			)
		})
		return
	}
	m.Results.WithLabelValues(result).Inc()
}

// CRMigrationSlotMetricsSingleton is set from cmd/manager/main.go during initialization.
var CRMigrationSlotMetricsSingleton *CRMigrationSlotMetrics

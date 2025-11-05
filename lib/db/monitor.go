// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import "github.com/prometheus/client_golang/prometheus"

type Monitor struct {
	// An observer that checks how long SELECT queries take to run.
	selectTimer *prometheus.HistogramVec
}

func NewDBMonitor() Monitor {
	return Monitor{
		selectTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_db_select_duration_seconds",
			Help:    "Duration of SELECT queries in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"group", "query"}),
	}
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.selectTimer.Describe(ch)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.selectTimer.Collect(ch)
}

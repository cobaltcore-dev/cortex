// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import "github.com/prometheus/client_golang/prometheus"

type monitor struct {
	connectionAttempts *prometheus.CounterVec
	connectionFailures prometheus.Counter
	connectionsActive  prometheus.Gauge

	// An observer that checks how long SELECT queries take to run.
	selectTimer *prometheus.HistogramVec
}

func newMonitor() monitor {
	return monitor{
		connectionAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_db_connection_attempts_total",
			Help: "Total number of database connection attempts",
		}, []string{"host", "database"}),
		selectTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_db_select_duration_seconds",
			Help:    "Duration of SELECT queries in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"group", "query"}),
	}
}

func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	m.selectTimer.Describe(ch)
}

func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.selectTimer.Collect(ch)
}

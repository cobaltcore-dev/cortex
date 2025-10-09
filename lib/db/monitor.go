// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	connectionAttempts prometheus.Counter
	// An observer that checks how long SELECT queries take to run.
	selectTimer *prometheus.HistogramVec
}

func NewDBMonitor(registry *monitoring.Registry) Monitor {
	connectionAttempts := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_db_connection_attempts_total",
		Help: "Total number of attempts to connect to the database",
	})
	selectTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_db_select_duration_seconds",
		Help:    "Duration of SELECT queries in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"group", "query"})
	registry.MustRegister(
		connectionAttempts,
		selectTimer,
	)
	return Monitor{
		connectionAttempts: connectionAttempts,
		selectTimer:        selectTimer,
	}
}

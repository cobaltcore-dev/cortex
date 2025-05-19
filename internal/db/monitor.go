// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	connectionAttempts prometheus.Counter
}

func NewDBMonitor(registry *monitoring.Registry) Monitor {
	connectionAttempts := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_db_connection_attempts_total",
		Help: "Total number of attempts to connect to the database",
	})
	registry.MustRegister(connectionAttempts)
	return Monitor{
		connectionAttempts: connectionAttempts,
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/prometheus/client_golang/prometheus"
)

// Each kpi plugin must conform to this interface.
type KPI interface {
	// Configure the kpi with a database, options, and the
	// registry to publish metrics on.
	Init(db db.DB, opts conf.RawOpts) error
	// Collect the kpi from the given data.
	Collect(ch chan<- prometheus.Metric)
	// Describe this metric.
	Describe(ch chan<- *prometheus.Desc)
	// Get the name of this kpi.
	// This name is used to identify the kpi in metrics, config, logs, etc.
	// Should be something like: "my_cool_kpi".
	GetName() string
}

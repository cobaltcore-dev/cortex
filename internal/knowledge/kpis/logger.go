// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type kpilogger struct {
	// Wrapped kpi to execute.
	kpi plugins.KPI
}

func (l kpilogger) Describe(ch chan<- *prometheus.Desc) {
	slog.Info("kpi: describing", "name", l.kpi.GetName())
	l.kpi.Describe(ch)
}

func (l kpilogger) Collect(ch chan<- prometheus.Metric) {
	slog.Info("kpi: collecting", "name", l.kpi.GetName())
	l.kpi.Collect(ch)
	slog.Info("kpi: collected", "name", l.kpi.GetName())
}

func (l kpilogger) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	slog.Info("kpi: initializing", "name", l.kpi.GetName())
	return l.kpi.Init(db, client, opts)
}

func (l kpilogger) GetName() string {
	return l.kpi.GetName()
}

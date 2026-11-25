// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("kpis")

type kpilogger struct {
	// Wrapped kpi to execute.
	kpi plugins.KPI
}

func (l kpilogger) Describe(ch chan<- *prometheus.Desc) {
	log.V(1).Info("kpi: describing", "name", l.kpi.GetName())
	l.kpi.Describe(ch)
}

func (l kpilogger) Collect(ch chan<- prometheus.Metric) {
	log.Info("kpi: collecting", "name", l.kpi.GetName())
	l.kpi.Collect(ch)
	log.Info("kpi: collected", "name", l.kpi.GetName())
}

func (l kpilogger) Init(inputs plugins.Inputs, client client.Client, opts conf.RawOpts) error {
	log.Info("kpi: initializing", "name", l.kpi.GetName())
	return l.kpi.Init(inputs, client, opts)
}

func (l kpilogger) GetName() string {
	return l.kpi.GetName()
}

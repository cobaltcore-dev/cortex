// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

// Configuration of supported kpis.
var SupportedKPIs = []plugins.KPI{
	// VMware kpis.
	&vmware.VMwareHostContentionKPI{},
	&vmware.VMwareProjectNoisinessKPI{},
	// NetApp kpis.
	&netapp.NetAppStoragePoolCPUUsageKPI{},
	// Shared kpis.
	&shared.HostUtilizationKPI{},
	&shared.VMMigrationStatisticsKPI{},
	&shared.VMLifeSpanKPI{},
	&shared.VMCommitmentsKPI{},
}

// Pipeline that extracts kpis from the database.
type KPIPipeline struct {
	// Config to use for the kpis.
	config conf.KPIsConfig
}

// Create a new kpi pipeline with kpis contained in the configuration.
func NewPipeline(config conf.KPIsConfig) KPIPipeline {
	return KPIPipeline{config: config}
}

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

// Initialize the kpi pipeline with the given database and registry.
func (p *KPIPipeline) Init(kpis []plugins.KPI, db db.DB, registry *monitoring.Registry) error {
	supportedKPIsByName := make(map[string]plugins.KPI)
	for _, kpi := range kpis {
		supportedKPIsByName[kpi.GetName()] = kpi
	}
	// Load all kpis from the configuration.
	for _, kpiConf := range p.config.Plugins {
		kpi, ok := supportedKPIsByName[kpiConf.Name]
		if !ok {
			return fmt.Errorf("kpi %s not supported", kpiConf.Name)
		}
		if err := kpi.Init(db, kpiConf.Options); err != nil {
			return fmt.Errorf("failed to initialize kpi %s: %w", kpiConf.Name, err)
		}
		registry.MustRegister(kpilogger{kpi: kpi})
		slog.Info(
			"kpi: added kpi",
			"name", kpiConf.Name,
			"options", kpiConf.Options,
		)
	}
	return nil
}

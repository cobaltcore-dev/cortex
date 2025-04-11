// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"log/slog"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
)

// Configuration of supported kpis.
var SupportedKPIs = []plugins.KPI{
	// Shared kpis.
	&shared.VMMigrationStatisticsKPI{},
	&shared.VMLifeSpanKPI{},
}

// Pipeline that extracts kpis from the database.
type KPIPipeline struct {
	// Loaded KPIs to calculate.
	kpis []plugins.KPI
	// Config to use for the kpis.
	config conf.KPIsConfig
}

// Create a new kpi pipeline with kpis contained in the configuration.
func NewPipeline(config conf.KPIsConfig) KPIPipeline {
	return KPIPipeline{config: config}
}

// Initialize the kpi pipeline with the given database and registry.
func (p *KPIPipeline) Init(db db.DB, registry *monitoring.Registry) error {
	supportedKPIsByName := make(map[string]plugins.KPI)
	for _, kpi := range SupportedKPIs {
		supportedKPIsByName[kpi.GetName()] = kpi
	}
	p.kpis = nil
	// Load all kpis from the configuration.
	for _, kpiConf := range p.config.Plugins {
		kpi, ok := supportedKPIsByName[kpiConf.Name]
		if !ok {
			panic("unknown kpi: " + kpiConf.Name)
		}
		if err := kpi.Init(db, kpiConf.Options, registry); err != nil {
			panic("failed to initialize kpi: " + err.Error())
		}
		p.kpis = append(p.kpis, kpi)
		slog.Info(
			"kpi: added kpi",
			"name", kpiConf.Name,
			"options", kpiConf.Options,
		)
	}
	return nil
}

// Calculate the kpis from the database.
func (p *KPIPipeline) Calculate() {
	// Execute all kpis in parallel.
	var wg sync.WaitGroup
	for _, kpi := range p.kpis {
		wg.Add(1)
		go func(kpi plugins.KPI) {
			defer wg.Done()
			slog.Info("updating kpi", "name", kpi.GetName())
			if err := kpi.Update(); err != nil {
				slog.Error("failed to update kpi", "name", kpi.GetName(), "error", err)
			}
			slog.Info("updated kpi", "name", kpi.GetName())
		}(kpi)
	}
	wg.Wait()
	slog.Info("all kpis updated")
}

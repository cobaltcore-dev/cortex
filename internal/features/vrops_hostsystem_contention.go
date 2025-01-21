// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10/orm"
	"github.com/prometheus/client_golang/prometheus"
)

type VROpsHostsystemContention struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName        struct{} `pg:"feature_vrops_hostsystem_contention"`
	ComputeHost      string   `pg:"compute_host,notnull"`
	AvgCPUContention float64  `pg:"avg_cpu_contention,notnull"`
	MaxCPUContention float64  `pg:"max_cpu_contention,notnull"`
}

type vROpsHostsystemContentionExtractor struct {
	DB                db.DB
	extractionCounter prometheus.Counter
	extractionTimer   prometheus.Histogram
}

func NewVROpsHostsystemContentionExtractor(db db.DB) FeatureExtractor {
	extractionCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_feature_vrops_hostsystem_contention_extract_runs",
		Help: "Total number of vROps hostsystem contention extractions",
	})
	extractionTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_feature_vrops_hostsystem_contention_extract_duration_seconds",
		Help:    "Duration of vROps hostsystem contention extraction",
		Buckets: prometheus.DefBuckets,
	})
	prometheus.MustRegister(extractionCounter, extractionTimer)
	return &vROpsHostsystemContentionExtractor{
		DB:                db,
		extractionCounter: extractionCounter,
		extractionTimer:   extractionTimer,
	}
}

// Create the feature schema.
func (e *vROpsHostsystemContentionExtractor) Init() error {
	if err := e.DB.Get().Model((*VROpsHostsystemContention)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

// Extract CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *vROpsHostsystemContentionExtractor) Extract() error {
	if e.extractionCounter != nil {
		e.extractionCounter.Inc()
	}
	if e.extractionTimer != nil {
		timer := prometheus.NewTimer(e.extractionTimer)
		defer timer.ObserveDuration()
	}

	logging.Log.Info("calculating hostsystem contention")
	// Delete the old data in the same transaction.
	tx, err := e.DB.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_vrops_hostsystem_contention"); err != nil {
		return tx.Rollback()
	}
	if _, err := tx.Exec(`
		INSERT INTO feature_vrops_hostsystem_contention (compute_host, avg_cpu_contention, max_cpu_contention)
		SELECT
			h.nova_compute_host AS compute_host,
			AVG(m.value) AS avg_cpu_contention,
			MAX(m.value) AS max_cpu_contention
		FROM vrops_host_metrics m
		JOIN feature_vrops_resolved_hostsystem h ON m.hostsystem = h.vrops_hostsystem
		WHERE m.name = 'vrops_hostsystem_cpu_contention_percentage'
		GROUP BY h.nova_compute_host;
    `); err != nil {
		return tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10/orm"
	"github.com/prometheus/client_golang/prometheus"
)

type ResolvedVROpsHostsystem struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName       struct{} `pg:"feature_vrops_resolved_hostsystem"`
	VROpsHostsystem string   `pg:"vrops_hostsystem,notnull"`
	NovaComputeHost string   `pg:"nova_compute_host,notnull"`
}

type vropsHostsystemResolver struct {
	DB                db.DB
	extractionCounter prometheus.Counter
	extractionTimer   prometheus.Histogram
}

func NewVROpsHostsystemResolver(db db.DB) FeatureExtractor {
	extractionCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_feature_vrops_resolved_hostsystem_extract_runs",
		Help: "Total number of vROps hostsystem resolutions",
	})
	extractionTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_feature_vrops_resolved_hostsystem_extract_duration_seconds",
		Help:    "Duration of vROps hostsystem resolution",
		Buckets: prometheus.DefBuckets,
	})
	prometheus.MustRegister(extractionCounter, extractionTimer)
	return &vropsHostsystemResolver{
		DB:                db,
		extractionCounter: extractionCounter,
		extractionTimer:   extractionTimer,
	}
}

// Create the feature schema.
func (e *vropsHostsystemResolver) Init() error {
	if err := e.DB.Get().Model((*ResolvedVROpsHostsystem)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

// Resolve vROps hostsystems to Nova compute hosts.
func (e *vropsHostsystemResolver) Extract() error {
	if e.extractionCounter != nil {
		e.extractionCounter.Inc()
	}
	if e.extractionTimer != nil {
		timer := prometheus.NewTimer(e.extractionTimer)
		defer timer.ObserveDuration()
	}

	logging.Log.Info("resolving vROps hostsystems")
	// Delete the old data in the same transaction.
	tx, err := e.DB.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_vrops_resolved_hostsystem"); err != nil {
		return tx.Rollback()
	}
	if _, err := tx.Exec(`
		INSERT INTO feature_vrops_resolved_hostsystem (vrops_hostsystem, nova_compute_host)
		SELECT
			m.hostsystem AS hostsystem,
			h.service_host AS service_host
		FROM vrops_vm_metrics m
		JOIN openstack_servers s ON m.instance_uuid = s.id
		JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
		GROUP BY m.hostsystem, h.service_host;
    `); err != nil {
		return tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

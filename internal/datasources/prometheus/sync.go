// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"errors"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

type Syncer interface {
	Init()
	Sync()
}

type syncer[M PrometheusMetric] struct {
	// The time range to sync the metrics in.
	SyncTimeRange time.Duration
	// The sync interval for the metrics.
	SyncInterval time.Duration
	// The resolution of the metrics to sync.
	// Note: this needs to be larger than the sampling rate of the metric.
	SyncResolutionSeconds int
	// Wait time between syncs to not overload the Prometheus server.
	SyncTimeout time.Duration

	MetricName    string
	PrometheusAPI PrometheusAPI[M]
	DB            db.DB
}

func NewSyncers(db db.DB) []Syncer {
	return []Syncer{
		NewSyncer[*VROpsVMMetric](db, "vrops_virtualmachine_cpu_demand_ratio"),
		NewSyncer[*VROpsHostMetric](db, "vrops_hostsystem_cpu_contention_percentage"),
	}
}

func NewSyncer[M PrometheusMetric](db db.DB, metricName string) Syncer {
	return &syncer[M]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		SyncTimeout:           10 * time.Second,

		MetricName:    metricName,
		PrometheusAPI: NewPrometheusAPI[M](),
		DB:            db,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer[M]) Init() {
	var model M
	if err := s.DB.Get().Model(model).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
}

// Get the start of the sync window for the given metric.
// The start window is either 4 weeks in the past or the
// latest metrics timestamp in the database.
func (s *syncer[M]) getSyncWindowStart() (time.Time, error) {
	// Check if there are any metrics in the database.
	var model M
	tableName := model.GetTableName()
	var nRows int
	if _, err := s.DB.Get().QueryOne(
		pg.Scan(&nRows),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = ?", tableName),
		s.MetricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %w", err)
	}
	logging.Log.Debug("number of rows", "nRows", nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start <timeRange> in the past.
		start := time.Now().Add(-s.SyncTimeRange)
		return start, nil
	}
	var latestTimestamp time.Time
	if _, err := s.DB.Get().QueryOne(
		pg.Scan(&latestTimestamp),
		fmt.Sprintf("SELECT MAX(timestamp) FROM %s WHERE name = ?", tableName),
		s.MetricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest timestamp: %w", err)
	}
	if latestTimestamp.IsZero() {
		return time.Time{}, errors.New("latestTimestamp is zero")
	}
	logging.Log.Debug("latest timestamp", "latestTimestamp", latestTimestamp)
	return latestTimestamp, nil
}

// Sync the given metric from Prometheus.
// The sync is done in intervals of 24 hours. We start from the given start time
// and sync recursively until we are caught up with the current time. Metrics
// outside of the window are deleted.
func (s *syncer[M]) sync(start time.Time) {
	// Sync full days only.
	end := start.Add(s.SyncInterval)
	if start.After(time.Now()) || end.After(time.Now()) {
		return // Finished syncing.
	}

	var model M
	tableName := model.GetTableName()
	logging.Log.Info(
		"syncing Prometheus data", "metricName", s.MetricName,
		"start", start, "end", end, "tableName", tableName,
	)
	// Drop all metrics that are older than 4 weeks.
	result, err := s.DB.Get().Exec(
		fmt.Sprintf("DELETE FROM %s WHERE name = ? AND timestamp < ?", tableName),
		s.MetricName, time.Now().Add(-s.SyncTimeRange),
	)
	if err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	logging.Log.Info("deleted old metrics", "rows", result.RowsAffected())
	// Fetch the metrics from Prometheus.
	prometheusData, err := s.PrometheusAPI.FetchMetrics(
		s.MetricName, start, end, s.SyncResolutionSeconds,
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	// Insert in smaller batches to avoid OOM issues.
	batchSize := 100
	for i := 0; i < len(prometheusData.Metrics); i += batchSize {
		metrics := prometheusData.Metrics[i:min(i+batchSize, len(prometheusData.Metrics))]
		if _, err = s.DB.Get().Model(&metrics).Insert(); err != nil {
			fmt.Printf("Failed to insert metrics: %v\n", err)
		}
	}
	logging.Log.Info("synced Prometheus data", "metrics", len(prometheusData.Metrics), "start", start, "end", end)

	// Don't overload the Prometheus server.
	time.Sleep(s.SyncTimeout)
	// Continue syncing.
	s.sync(end)
}

// Sync the Prometheus metrics with the database.
func (s *syncer[M]) Sync() {
	// Sync this metric until we are caught up.
	start, err := s.getSyncWindowStart()
	if err != nil {
		logging.Log.Error("failed to get sync window start", "error", err)
		return
	}
	s.sync(start)
	time.Sleep(s.SyncTimeout)
}

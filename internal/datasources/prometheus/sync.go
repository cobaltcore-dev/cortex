// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"errors"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

var (
	// The time range to sync the metrics in.
	syncTimeRange = 4 * 7 * 24 * time.Hour // 4 weeks
	// The sync interval for the metrics.
	syncInterval = 24 * time.Hour
	// The resolution of the metrics to sync.
	// Note: this needs to be larger than the sampling rate of the metric.
	syncResolutionSeconds = 12 * 60 * 60 // 12 hours (2 datapoints per day per metric)
	// Wait time between syncs to not overload the Prometheus server.
	syncTimeout = 10 * time.Second
	// VMware vROps metrics to sync from the Prometheus server.
	metrics = []string{
		"vrops_virtualmachine_cpu_demand_ratio",
	}
)

// Get the start of the sync window for the given metric.
// The start window is either 4 weeks in the past or the
// latest metrics timestamp in the database.
func getSyncWindowStart(metricName string) (time.Time, error) {
	// Check if there are any metrics in the database.
	var nRows int
	if _, err := db.Get().QueryOne(
		pg.Scan(&nRows),
		"SELECT COUNT(*) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %w", err)
	}
	logging.Log.Debug("number of rows", "nRows", nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start <timeRange> in the past.
		start := time.Now().Add(-syncTimeRange)
		return start, nil
	}
	var latestTimestamp time.Time
	if _, err := db.Get().QueryOne(
		pg.Scan(&latestTimestamp),
		"SELECT MAX(timestamp) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest timestamp: %w", err)
	}
	if latestTimestamp.IsZero() {
		return time.Time{}, errors.New("latestTimestamp is zero")
	}
	logging.Log.Debug("latest timestamp", "latestTimestamp", latestTimestamp)
	return latestTimestamp, nil
}

// Create the necessary database tables if they do not exist.
func Init() {
	if err := db.Get().Model((*PrometheusMetric)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
}

// Sync the given metric from Prometheus.
// The sync is done in intervals of 24 hours. We start from the given start time
// and sync recursively until we are caught up with the current time. Metrics
// outside of the window are deleted.
func sync(start time.Time, metricName string) {
	// Sync full days only.
	end := start.Add(syncInterval)
	if start.After(time.Now()) || end.After(time.Now()) {
		return // Finished syncing.
	}

	logging.Log.Info("syncing Prometheus data", "metricName", metricName, "start", start, "end", end)
	// Drop all metrics that are older than 4 weeks.
	result, err := db.Get().Exec(
		"DELETE FROM metrics WHERE name = ? AND timestamp < ?",
		metricName,
		time.Now().Add(-syncTimeRange),
	)
	if err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	logging.Log.Info("deleted old metrics", "rows", result.RowsAffected())
	// Fetch the metrics from Prometheus.
	prometheusData, err := fetchMetrics(
		conf.Get().PrometheusURL, metricName,
		start, end, syncResolutionSeconds,
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	// Insert in smaller batches to avoid OOM issues.
	batchSize := 100
	for i := 0; i < len(prometheusData.Metrics); i += batchSize {
		metrics := prometheusData.Metrics[i:min(i+batchSize, len(prometheusData.Metrics))]
		if _, err = db.Get().Model(&metrics).Insert(); err != nil {
			fmt.Printf("Failed to insert metrics: %v\n", err)
		}
	}
	logging.Log.Info("synced Prometheus data", "metrics", len(prometheusData.Metrics), "start", start, "end", end)

	// Don't overload the Prometheus server.
	time.Sleep(syncTimeout)
	// Continue syncing.
	sync(end, metricName)
}

// Sync the Prometheus metrics with the database.
func Sync() {
	for _, metricName := range metrics {
		// Sync this metric until we are caught up.
		start, err := getSyncWindowStart(metricName)
		if err != nil {
			logging.Log.Error("failed to get sync window start", "error", err)
			continue
		}
		sync(start, metricName)
		time.Sleep(syncTimeout)
	}
}

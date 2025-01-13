// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

func getSyncWindowStart(metricName string) (time.Time, error) {
	// Check if there are any metrics in the database.
	var nRows int
	if _, err := db.DB.QueryOne(
		pg.Scan(&nRows),
		"SELECT COUNT(*) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %v", err)
	}
	logging.Log.Debug("number of rows", "nRows", nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start 4 weeks in the past.
		start := time.Now().Add(-4 * 7 * 24 * time.Hour)
		return start, nil
	}
	var latestTimestamp time.Time
	if _, err := db.DB.QueryOne(
		pg.Scan(&latestTimestamp),
		"SELECT MAX(timestamp) FROM metrics WHERE name = ?",
		metricName,
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest timestamp: %v", err)
	}
	if latestTimestamp.IsZero() {
		return time.Time{}, fmt.Errorf("latestTimestamp is zero")
	}
	logging.Log.Debug("latest timestamp", "latestTimestamp", latestTimestamp)
	return latestTimestamp, nil
}

func sync(
	start time.Time,
	interval time.Duration,
	resolutionSeconds int,
	metricName string,
) {
	// Sync full days only.
	end := start.Add(interval)
	if start.After(time.Now()) || end.After(time.Now()) {
		return // Finished syncing.
	}

	logging.Log.Info("syncing Prometheus data", "metricName", metricName, "start", start, "end", end)
	// Drop all metrics that are older than 4 weeks.
	result, err := db.DB.Exec(
		"DELETE FROM metrics WHERE name = ? AND timestamp < ?",
		metricName,
		time.Now().Add(-4*7*24*time.Hour),
	)
	if err != nil {
		fmt.Printf("Failed to delete old metrics: %v\n", err)
		return
	}
	logging.Log.Info("deleted old metrics", "rows", result.RowsAffected())
	// Fetch the metrics from Prometheus.
	prometheusData, err := fetchMetrics(
		conf.Get().PrometheusUrl,
		metricName,
		start,
		end,
		resolutionSeconds,
	)
	if err != nil {
		fmt.Printf("Failed to fetch metrics: %v\n", err)
		return
	}
	db.DB.Model(&prometheusData.Metrics).Insert()
	logging.Log.Info("synced Prometheus data", "metrics", len(prometheusData.Metrics), "start", start, "end", end)

	// Don't overload the Prometheus server.
	time.Sleep(60 * time.Second)
	// Continue syncing.
	sync(end, interval, resolutionSeconds, metricName)
}

func Init() {
	if err := db.DB.Model((*PrometheusMetric)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
}

func Sync() {
	metrics := []string{
		"vrops_virtualmachine_cpu_demand_ratio",
	}

	for _, metricName := range metrics {
		// Sync this metric until we are caught up.
		start, err := getSyncWindowStart(metricName)
		if err != nil {
			logging.Log.Error("failed to get sync window start", "error", err)
			continue
		}
		sync(
			start,
			24*time.Hour,
			// Needs to be larger than the sampling rate of the metric.
			12*60*60, // 12 hours (2 datapoints per day per metric)
			metricName,
		)
	}
}

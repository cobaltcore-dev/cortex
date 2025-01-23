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
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

// Prometheus syncer for an arbitrary prometheus metric model.
type syncer[M PrometheusMetric] struct {
	// The time range to sync the metrics in.
	SyncTimeRange time.Duration
	// The sync interval for the metrics.
	SyncInterval time.Duration
	// The resolution of the metrics to sync.
	// Note: this needs to be larger than the sampling rate of the metric.
	SyncResolutionSeconds int
	// The name of the metric to sync.
	MetricName string
	// The Prometheus API endpoint to fetch the metrics.
	PrometheusAPI PrometheusAPI[M]
	// The database to store the metrics in.
	DB db.DB

	monitor sync.Monitor
}

// Syncer that syncs all configured metrics.
type CombinedSyncer struct {
	Syncers []sync.Datasource
	monitor sync.Monitor
}

// Create multiple syncers configured by the external service configuration.
func NewCombinedSyncer(config conf.Config, db db.DB, monitor sync.Monitor) sync.Datasource {
	moduleConfig := config.GetSyncConfig().Prometheus
	logging.Log.Info("loading syncers", "metrics", moduleConfig.Metrics)
	syncers := []sync.Datasource{}
	for _, metricConfig := range moduleConfig.Metrics {
		syncers = append(syncers, newSyncer(db, metricConfig, monitor))
	}
	return CombinedSyncer{
		Syncers: syncers,
		monitor: monitor,
	}
}

func (s CombinedSyncer) Init() {
	for _, syncer := range s.Syncers {
		syncer.Init()
	}
}

func (s CombinedSyncer) Sync() {
	if s.monitor.PipelineRunTimer != nil {
		hist := s.monitor.PipelineRunTimer.WithLabelValues("prometheus")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	for _, syncer := range s.Syncers {
		syncer.Sync()
	}
}

// Create a new syncer for the given metric configuration.
// This function maps the given metric type to the implemented golang type.
func newSyncer(db db.DB, c conf.SyncPrometheusMetricConfig, monitor sync.Monitor) sync.Datasource {
	switch c.Type {
	case "vrops_vm_metric":
		return newSyncerOfType[*VROpsVMMetric](db, c, monitor)
	case "vrops_host_metric":
		return newSyncerOfType[*VROpsHostMetric](db, c, monitor)
	default:
		panic("unknown metric type: " + c.Type)
	}
}

// Create a new syncer for the given metric type.
// If no custom metrics granularity is set, the default values are used.
func newSyncerOfType[M PrometheusMetric](
	db db.DB,
	c conf.SyncPrometheusMetricConfig,
	monitor sync.Monitor,
) sync.Datasource {
	// Set default values if none are provided.
	var timeRangeSeconds = 2419200 // 4 weeks
	if c.TimeRangeSeconds != nil {
		timeRangeSeconds = *c.TimeRangeSeconds
	}
	var intervalSeconds = 86400 // 1 day
	if c.IntervalSeconds != nil {
		intervalSeconds = *c.IntervalSeconds
	}
	var resolutionSeconds = 43200 // 12 hours
	if c.ResolutionSeconds != nil {
		resolutionSeconds = *c.ResolutionSeconds
	}

	return &syncer[M]{
		SyncTimeRange:         time.Duration(timeRangeSeconds) * time.Second,
		SyncInterval:          time.Duration(intervalSeconds) * time.Second,
		SyncResolutionSeconds: resolutionSeconds,
		MetricName:            c.Name,
		PrometheusAPI:         NewPrometheusAPI[M](c.Name, monitor),
		DB:                    db,
		monitor:               monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer[M]) Init() {
	logging.Log.Info("initializing syncer", "metricName", s.MetricName)
	var model M
	if err := s.DB.Get().Model(model).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
	logging.Log.Info("created table", "tableName", model.GetTableName())
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
// The sync is done in intervals. We start from the given start time
// and sync recursively until we are caught up with the current time.
// Metrics outside of the window are deleted.
func (s *syncer[M]) sync(start time.Time) {
	// Sync full intervals only.
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
		logging.Log.Error("failed to delete old metrics", "error", err)
		return
	}
	logging.Log.Info("deleted old metrics", "rows", result.RowsAffected())
	// Fetch the metrics from Prometheus.
	prometheusData, err := s.PrometheusAPI.FetchMetrics(
		s.MetricName, start, end, s.SyncResolutionSeconds,
	)
	if err != nil {
		logging.Log.Error("failed to fetch metrics", "error", err)
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
	logging.Log.Info("synced Prometheus data", "newMetrics", len(prometheusData.Metrics), "start", start, "end", end)

	// Count rows for the gauge.
	var nRows int
	if _, err := s.DB.Get().QueryOne(
		pg.Scan(&nRows),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = ?", tableName),
		s.MetricName,
	); err == nil {
		logging.Log.Info("counted metrics", "nRows", nRows, "metricName", s.MetricName)
		if s.monitor.PipelineObjectsGauge != nil {
			s.monitor.PipelineObjectsGauge.
				WithLabelValues("prometheus_" + s.MetricName).
				Set(float64(nRows))
		}
	} else {
		logging.Log.Error("failed to count metrics rows", "error", err)
	}

	// Don't overload the Prometheus server.
	time.Sleep(1 * time.Second)
	// Continue syncing.
	s.sync(end)
}

// Sync the Prometheus metrics with the database.
func (s *syncer[M]) Sync() {
	logging.Log.Info("syncing metrics", "metricName", s.MetricName)
	// Sync this metric until we are caught up.
	start, err := s.getSyncWindowStart()
	if err != nil {
		logging.Log.Error("failed to get sync window start", "error", err)
		return
	}
	s.sync(start)
	logging.Log.Info("synced metrics", "metricName", s.MetricName)
}

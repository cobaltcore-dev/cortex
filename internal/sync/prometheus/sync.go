// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"errors"
	"fmt"
	"log/slog"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
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
func NewCombinedSyncer(config conf.SyncPrometheusConfig, db db.DB, monitor sync.Monitor) sync.Datasource {
	slog.Info("loading syncers", "metrics", config.Metrics)
	syncers := []sync.Datasource{}
	hostConfByName := make(map[string]conf.SyncPrometheusHostConfig)
	for _, hostConf := range config.Hosts {
		hostConfByName[hostConf.Name] = hostConf
	}
	for _, metricConfig := range config.Metrics {
		syncerFunc, ok := supportedTypes[metricConfig.Type]
		if !ok {
			panic("unsupported metric type: " + metricConfig.Type)
		}
		hostConf, ok := hostConfByName[metricConfig.PrometheusName]
		if !ok {
			panic("unknown metric host: " + metricConfig.PrometheusName)
		}
		syncers = append(syncers, syncerFunc(db, hostConf, metricConfig, monitor))
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

	// Sync all metrics in parallel.
	var wg gosync.WaitGroup
	for _, syncer := range s.Syncers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer.Sync()
		}()
	}
	wg.Wait()
}

// Create a new syncer for the given metric type.
// If no custom metrics granularity is set, the default values are used.
func newSyncerOfType[M PrometheusMetric](
	db db.DB,
	hostConf conf.SyncPrometheusHostConfig,
	metricConf conf.SyncPrometheusMetricConfig,
	monitor sync.Monitor,
) sync.Datasource {
	// Set default values if none are provided.
	var timeRangeSeconds = 2419200 // 4 weeks
	if metricConf.TimeRangeSeconds != nil {
		timeRangeSeconds = *metricConf.TimeRangeSeconds
	}
	var intervalSeconds = 86400 // 1 day
	if metricConf.IntervalSeconds != nil {
		intervalSeconds = *metricConf.IntervalSeconds
	}
	var resolutionSeconds = 43200 // 12 hours
	if metricConf.ResolutionSeconds != nil {
		resolutionSeconds = *metricConf.ResolutionSeconds
	}

	return &syncer[M]{
		SyncTimeRange:         time.Duration(timeRangeSeconds) * time.Second,
		SyncInterval:          time.Duration(intervalSeconds) * time.Second,
		SyncResolutionSeconds: resolutionSeconds,
		MetricName:            metricConf.Name,
		PrometheusAPI:         NewPrometheusAPI[M](hostConf, metricConf, monitor),
		DB:                    db,
		monitor:               monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer[M]) Init() {
	slog.Info("initializing syncer", "metricName", s.MetricName)
	var model M
	if err := s.DB.Get().Model(model).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
	slog.Info("created table", "tableName", model.GetTableName())
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
	slog.Debug("number of rows", "nRows", nRows)
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
	slog.Debug("latest timestamp", "latestTimestamp", latestTimestamp)
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
	slog.Info(
		"syncing Prometheus data", "metricName", s.MetricName,
		"start", start, "end", end, "tableName", tableName,
	)
	// Drop all metrics that are older than 4 weeks.
	result, err := s.DB.Get().Exec(
		fmt.Sprintf("DELETE FROM %s WHERE name = ? AND timestamp < ?", tableName),
		s.MetricName, time.Now().Add(-s.SyncTimeRange),
	)
	if err != nil {
		slog.Error("failed to delete old metrics", "error", err)
		return
	}
	slog.Info("deleted old metrics", "rows", result.RowsAffected())
	// Fetch the metrics from Prometheus.
	prometheusData, err := s.PrometheusAPI.FetchMetrics(
		s.MetricName, start, end, s.SyncResolutionSeconds,
	)
	if err != nil {
		slog.Error("failed to fetch metrics", "error", err)
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
	slog.Info("synced Prometheus data", "newMetrics", len(prometheusData.Metrics), "start", start, "end", end)

	// Don't overload the Prometheus server.
	time.Sleep(1 * time.Second)
	// Continue syncing.
	s.sync(end)
}

// Count metrics in the database and update the gauge.
func (s *syncer[M]) countMetrics() {
	// Count rows for the gauge.
	var model M
	var nRows int
	if _, err := s.DB.Get().QueryOne(
		pg.Scan(&nRows),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = ?", model.GetTableName()),
		s.MetricName,
	); err == nil {
		slog.Info("counted metrics", "nRows", nRows, "metricName", s.MetricName)
		if s.monitor.PipelineObjectsGauge != nil {
			s.monitor.PipelineObjectsGauge.
				WithLabelValues("prometheus_" + s.MetricName).
				Set(float64(nRows))
		}
	} else {
		slog.Error("failed to count metrics rows", "error", err)
	}
}

// Sync the Prometheus metrics with the database.
func (s *syncer[M]) Sync() {
	// Make sure to count the metrics after everything is done,
	// even when no new metrics were consumed.
	defer s.countMetrics()

	slog.Info("syncing metrics", "metricName", s.MetricName)
	// Sync this metric until we are caught up.
	start, err := s.getSyncWindowStart()
	if err != nil {
		slog.Error("failed to get sync window start", "error", err)
		return
	}
	s.sync(start)
	slog.Info("synced metrics", "metricName", s.MetricName)
}

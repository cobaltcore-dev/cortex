// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

// List of supported metric types that can be specified in the yaml config.
var supportedSyncers = map[string]func(
	db.DB,
	conf.SyncPrometheusHostConfig,
	conf.SyncPrometheusMetricConfig,
	sync.Monitor,
) Syncer{
	"vrops_host_metric":    newSyncerOfType[VROpsHostMetric],
	"vrops_vm_metric":      newSyncerOfType[VROpsVMMetric],
	"node_exporter_metric": newSyncerOfType[NodeExporterMetric],
}

// Syncer that syncs all configured metrics.
type CombinedSyncer struct {
	syncers []Syncer
	monitor sync.Monitor
	// MQTT client to publish mqtt data.
	mqttClient mqtt.Client
}

// Create multiple syncers configured by the external service configuration.
func NewCombinedSyncer(config conf.SyncPrometheusConfig, db db.DB, monitor sync.Monitor) sync.Datasource {
	slog.Info("loading syncers", "metrics", config.Metrics)
	syncers := []Syncer{}
	for _, metricConfig := range config.Metrics {
		syncerFunc, ok := supportedSyncers[metricConfig.Type]
		if !ok {
			panic("unsupported metric type: " + metricConfig.Type)
		}
		// Get the prometheuses to sync this metric from.
		for _, hostConf := range config.Hosts {
			for _, providedMetricType := range hostConf.ProvidedMetricTypes {
				if providedMetricType == metricConfig.Type {
					slog.Info("adding syncer", "metricType", metricConfig.Type, "host", hostConf.Name)
					syncers = append(syncers, syncerFunc(db, hostConf, metricConfig, monitor))
				}
			}
		}
	}
	return CombinedSyncer{
		syncers:    syncers,
		monitor:    monitor,
		mqttClient: mqtt.NewClient(),
	}
}

// Initialize all nested syncers.
func (s CombinedSyncer) Init(ctx context.Context) {
	for _, syncer := range s.syncers {
		syncer.Init(ctx)
	}
}

// Sync all metrics in parallel and publish triggers.
func (s CombinedSyncer) Sync(context context.Context) {
	if s.monitor.PipelineRunTimer != nil {
		hist := s.monitor.PipelineRunTimer.WithLabelValues("prometheus")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	var wg gosync.WaitGroup
	for _, syncer := range s.syncers {
		wg.Add(1)
		go func(syncer Syncer) {
			defer wg.Done()
			syncer.Sync(context)
			for _, trigger := range syncer.Triggers() {
				go s.mqttClient.Publish(trigger, "")
			}
		}(syncer)
	}
	wg.Wait()
}

type Syncer interface {
	sync.Datasource
	// Get triggers produced by this syncer.
	Triggers() []string
}

// Prometheus syncer for an arbitrary prometheus metric model.
type syncer[M PrometheusMetric] struct {
	// The time range to sync the metrics in.
	SyncTimeRange time.Duration
	// The sync interval for the metrics.
	SyncInterval time.Duration
	// The resolution of the metrics to sync.
	// Note: this needs to be larger than the sampling rate of the metric.
	SyncResolutionSeconds int
	// The metric conf.
	MetricConf conf.SyncPrometheusMetricConfig
	// The Prometheus API endpoint to fetch the metrics.
	PrometheusAPI PrometheusAPI[M]
	// The database to store the metrics in.
	DB db.DB
	// The sleep interval between syncs.
	sleepInterval time.Duration

	monitor sync.Monitor
}

// Create a new syncer for the given metric type.
// If no custom metrics granularity is set, the default values are used.
func newSyncerOfType[M PrometheusMetric](
	db db.DB,
	hostConf conf.SyncPrometheusHostConfig,
	metricConf conf.SyncPrometheusMetricConfig,
	monitor sync.Monitor,
) Syncer {
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
		MetricConf:            metricConf,
		PrometheusAPI:         NewPrometheusAPI[M](hostConf, metricConf, monitor),
		DB:                    db,
		monitor:               monitor,
		sleepInterval:         time.Second,
	}
}

// Get the triggers produced by this syncer.
func (s *syncer[M]) Triggers() []string {
	return []string{
		TriggerMetricAliasSynced(s.MetricConf.Alias),
		TriggerMetricTypeSynced(s.MetricConf.Type),
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer[M]) Init(ctx context.Context) {
	slog.Info("initializing syncer", "conf", s.MetricConf)
	var model M
	if err := s.DB.CreateTable(s.DB.AddTable(model)); err != nil {
		panic(err)
	}
}

// Get the start of the sync window for the given metric.
// The start window is either 4 weeks in the past or the
// latest metrics timestamp in the database.
func (s *syncer[M]) getSyncWindowStart() (time.Time, error) {
	// Check if there are any metrics in the database.
	var model M
	tableName := model.TableName()
	nRows, err := s.DB.SelectInt(
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = :name", tableName),
		map[string]any{"name": s.MetricConf.Alias},
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %w", err)
	}
	slog.Debug("number of rows", "nRows", nRows)
	if nRows == 0 {
		// No metrics in the database yet. Start <timeRange> in the past.
		start := time.Now().Add(-s.SyncTimeRange)
		return start, nil
	}
	if err := s.DB.SelectOne(
		&model,
		fmt.Sprintf(`
			SELECT name, timestamp FROM %s
			WHERE name = :name
			ORDER BY timestamp
			DESC LIMIT 1
		`, tableName),
		map[string]any{"name": s.MetricConf.Alias},
	); err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest timestamp: %w", err)
	}
	latestTimestamp := model.GetTimestamp()
	if latestTimestamp.IsZero() {
		return time.Time{}, errors.New("latestTimestamp is zero")
	}
	slog.Info("latest timestamp", "latestTimestamp", latestTimestamp)
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
	tableName := model.TableName()
	slog.Info(
		"syncing Prometheus data", "metricAlias", s.MetricConf.Alias,
		"start", start, "end", end, "tableName", tableName,
	)
	// Drop all metrics that are older than 4 weeks.
	result, err := s.DB.Exec(
		fmt.Sprintf("DELETE FROM %s WHERE name = :name AND timestamp < :timestamp", tableName),
		map[string]any{"name": s.MetricConf.Alias, "timestamp": time.Now().Add(-s.SyncTimeRange)},
	)
	if err != nil {
		slog.Error("failed to delete old metrics", "error", err)
		return
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		slog.Error("failed to get rows affected", "error", err)
		return
	}
	slog.Info("deleted old metrics", "rows", rowsAffected)
	// Fetch the metrics from Prometheus.
	prometheusData, err := s.PrometheusAPI.FetchMetrics(
		s.MetricConf.Query, start, end, s.SyncResolutionSeconds,
	)
	if err != nil {
		slog.Error("failed to fetch metrics", "error", err)
		return
	}
	if err := db.BulkInsert(s.DB, prometheusData.Metrics...); err != nil {
		slog.Error("failed to bulk insert metrics", "error", err)
		return
	}
	slog.Info(
		"synced Prometheus data", "newMetrics", len(prometheusData.Metrics),
		"metricAlias", s.MetricConf.Alias, "start", start, "end", end,
	)

	// Don't overload the Prometheus server.
	time.Sleep(s.sleepInterval)
	// Continue syncing.
	s.sync(end)
}

// Count metrics in the database and update the gauge.
func (s *syncer[M]) countMetrics() {
	// Count rows for the gauge.
	var model M
	if nRows, err := s.DB.SelectInt(
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = :name", model.TableName()),
		map[string]any{"name": s.MetricConf.Alias},
	); err == nil {
		slog.Info("counted metrics", "nRows", nRows, "metricAlias", s.MetricConf.Alias)
		if s.monitor.PipelineObjectsGauge != nil {
			s.monitor.PipelineObjectsGauge.
				WithLabelValues("prometheus_" + s.MetricConf.Alias).
				Set(float64(nRows))
		}
	} else {
		slog.Error("failed to count metrics rows", "error", err)
	}
}

// Sync the Prometheus metrics with the database.
func (s *syncer[M]) Sync(context context.Context) {
	// TODO: Add context cancellation.

	// Make sure to count the metrics after everything is done,
	// even when no new metrics were consumed.
	defer s.countMetrics()

	slog.Info("syncing metrics", "metricAlias", s.MetricConf.Alias)
	// Sync this metric until we are caught up.
	start, err := s.getSyncWindowStart()
	if err != nil {
		slog.Error("failed to get sync window start", "error", err)
		return
	}
	s.sync(start)
	slog.Info("synced metrics", "metricAlias", s.MetricConf.Alias)
}

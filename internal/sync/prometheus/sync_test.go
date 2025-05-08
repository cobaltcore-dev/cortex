// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go" // Correct alias for dto.Metric
)

type mockSyncer struct {
	syncCalled bool
}

func (m *mockSyncer) Init(ctx context.Context) {
	// Mock implementation
}

func (m *mockSyncer) Sync(ctx context.Context) {
	m.syncCalled = true
}

func (m *mockSyncer) Triggers() []string {
	return []string{"mock_trigger"}
}

func (m *mockSyncer) DatasourceType() string {
	return "mock"
}

type mockPrometheusAPI[M PrometheusMetric] struct {
	data prometheusTimelineData[M]
	err  error
}

func (api *mockPrometheusAPI[M]) FetchMetrics(
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*prometheusTimelineData[M], error) {
	// Return the error if set
	if api.err != nil {
		return nil, api.err
	}
	return &api.data, nil
}

func TestSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	syncer := &syncer[VROpsVMMetric]{
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: &mockPrometheusAPI[VROpsVMMetric]{},
		DB:            testDB,
	}
	syncer.Init(t.Context())

	// Verify the table was created
	if !testDB.TableExists(&VROpsVMMetric{}) {
		t.Error("expected table to be created")
	}
}

func TestSyncer_getSyncWindowStart(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Test case: No metrics in the database
	syncer := &syncer[VROpsVMMetric]{
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: &mockPrometheusAPI[VROpsVMMetric]{},
		DB:            testDB,
	}
	syncer.Init(t.Context())
	start, err := syncer.getSyncWindowStart()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expectedStart := time.Now().Add(-syncer.SyncTimeRange)
	if !start.Before(time.Now()) || !start.After(expectedStart.Add(-time.Minute)) {
		t.Errorf("expected start to be around %v, got %v", expectedStart, start)
	}

	// Test case: Metrics in the database
	latestTimestamp := time.Now().Add(-time.Hour)
	if _, err = testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp) VALUES (:metric, :timestamp)",
		map[string]any{"metric": "test_metric", "timestamp": latestTimestamp},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start, err = syncer.getSyncWindowStart()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Equal to the second to avoid floating point errors
	startSecondsUnix := start.Unix()
	latestTimestampSecondsUnix := latestTimestamp.Unix()
	if startSecondsUnix != latestTimestampSecondsUnix {
		t.Errorf("expected start to be %v, got %v", latestTimestamp, start)
	}
}

func TestSyncer_sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: []VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: mockPrometheusAPI,
		DB:            testDB,
	}
	syncer.Init(t.Context())

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify the metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 4*7 {
		// Mock returns 1 datapoint each time called
		t.Errorf("expected 4 weeks of datapoints, got %d", len(metrics))
	}
	if metrics[0].Name != "test_metric" {
		t.Errorf("expected metric name to be %s, got %s", "test_metric", metrics[0].Name)
	}
	if metrics[0].Value != 123.45 {
		t.Errorf("expected metric value to be %f, got %f", 123.45, metrics[0].Value)
	}
}

func TestSyncer_sync_Failure(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		err: errors.New("failed to fetch metrics"),
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: mockPrometheusAPI,
		DB:            testDB,
	}
	syncer.Init(t.Context())

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify no metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}

func TestSyncer_DeleteOldMetrics(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: []VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: mockPrometheusAPI,
		DB:            testDB,
	}
	syncer.Init(t.Context())

	// Insert old metrics
	oldTimestamp := time.Now().Add(-5 * 7 * 24 * time.Hour) // 5 weeks ago
	if _, err := testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp, value) VALUES (:name, :timestamp, :value)",
		map[string]any{"name": "test_metric", "timestamp": oldTimestamp, "value": 123.45},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert recent metrics
	recentTimestamp := time.Now().Add(-2 * 7 * 24 * time.Hour) // 2 weeks ago
	if _, err := testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp, value) VALUES (:name, :timestamp, :value)",
		map[string]any{"name": "test_metric", "timestamp": recentTimestamp, "value": 123.45},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify old metrics were deleted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT name, timestamp, value FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	for _, metric := range metrics {
		if metric.Timestamp.Before(time.Now().Add(-syncer.SyncTimeRange)) {
			t.Errorf("expected old metrics to be deleted, but found metric with timestamp %v", metric.Timestamp)
		}
	}
}

func TestSyncer_BenchmarkMemoryUsage(t *testing.T) {
	if os.Getenv("BENCHMARK") != "1" {
		t.Skip("skipping test; set BENCHMARK=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	dbEnv.TraceOff() // Will otherwise cause a lot of output
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create a mock Prometheus API that returns a large number of metrics
	largeMetricCount := 10000
	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: make([]VROpsVMMetric, largeMetricCount),
		},
	}
	for i := range largeMetricCount {
		mockPrometheusAPI.data.Metrics[i] = VROpsVMMetric{
			Name:      "test_metric",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Second),
			Value:     float64(i),
		}
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: mockPrometheusAPI,
		DB:            testDB,
	}
	syncer.Init(t.Context())

	// Measure memory usage before syncing
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Run the sync function
	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Measure memory usage after syncing
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	// Calculate memory usage
	allocatedMemory := memStatsAfter.Alloc - memStatsBefore.Alloc
	t.Logf("Memory used by sync function: %d bytes", allocatedMemory)

	// Verify the metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) == 0 {
		t.Error("expected metrics to be inserted, but found none")
	}

	// Ensure memory usage is within acceptable limits
	const memoryThreshold = 100 * 1024 * 1024
	if allocatedMemory > memoryThreshold {
		t.Errorf("memory usage exceeded threshold: %d bytes used, threshold is %d bytes", allocatedMemory, memoryThreshold)
	}
	slog.Debug("Memory usage within acceptable limits", "allocatedMemory", allocatedMemory, "threshold", memoryThreshold)
}

func TestNewCombinedSyncer(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockMonitor := sync.Monitor{}
	mockMQTTClient := &mqtt.MockClient{}

	config := conf.SyncPrometheusConfig{
		Metrics: []conf.SyncPrometheusMetricConfig{
			{Type: "vrops_host_metric", Alias: "test_metric"},
		},
		Hosts: []conf.SyncPrometheusHostConfig{
			{Name: "test_host", ProvidedMetricTypes: []string{"vrops_host_metric"}},
		},
	}

	supportedSyncers := map[string]syncerFunc{
		"vrops_host_metric": newSyncerOfType[VROpsHostMetric],
	}

	combinedSyncer := NewCombinedSyncer(supportedSyncers, config, testDB, mockMonitor, mockMQTTClient)
	if combinedSyncer == nil {
		t.Fatal("expected NewCombinedSyncer to return a valid CombinedSyncer")
	}
}

func TestCombinedSyncer_Sync(t *testing.T) {
	mockMonitor := sync.Monitor{}
	mockMQTTClient := &mqtt.MockClient{}

	mockSyncer := &mockSyncer{}
	combinedSyncer := CombinedSyncer{
		syncers:    []Syncer{mockSyncer},
		monitor:    mockMonitor,
		mqttClient: mockMQTTClient,
	}

	combinedSyncer.Sync(t.Context())
	if !mockSyncer.syncCalled {
		t.Error("expected Sync to be called on the mock syncer")
	}
}

func TestNewSyncerOfType(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockMonitor := sync.Monitor{}
	hostConf := conf.SyncPrometheusHostConfig{Name: "test_host"}
	metricConf := conf.SyncPrometheusMetricConfig{Alias: "test_metric"}

	syncer := newSyncerOfType[VROpsHostMetric](testDB, hostConf, metricConf, mockMonitor)
	if syncer == nil {
		t.Fatal("expected newSyncerOfType to return a valid syncer")
	}
}

func TestSyncer_Triggers(t *testing.T) {
	syncer := &syncer[VROpsVMMetric]{
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Type:  "vrops_vm_metric",
		},
	}

	triggers := syncer.Triggers()
	if len(triggers) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(triggers))
	}
	if triggers[0] != "triggers/sync/prometheus/alias/test_metric" {
		t.Errorf("unexpected trigger: %s", triggers[0])
	}
	if triggers[1] != "triggers/sync/prometheus/type/vrops_vm_metric" {
		t.Errorf("unexpected trigger: %s", triggers[1])
	}
}

func TestSyncer_CountMetrics(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockMonitor := sync.Monitor{
		PipelineObjectsGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "pipeline_objects",
				Help: "Number of pipeline objects",
			},
			[]string{"label"},
		),
	}

	syncer := &syncer[VROpsVMMetric]{
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
		},
		DB:      testDB,
		monitor: mockMonitor,
	}
	syncer.Init(t.Context())

	// Insert mock data
	if _, err := testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp, value) VALUES (:name, :timestamp, :value)",
		map[string]any{"name": "test_metric", "timestamp": time.Now(), "value": 123.45},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	syncer.countMetrics()
	// Verify the gauge was updated
	metric := &io_prometheus_client.Metric{}
	if err := mockMonitor.PipelineObjectsGauge.WithLabelValues("prometheus_test_metric").Write(metric); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if *metric.Gauge.Value != 1 {
		t.Errorf("expected gauge value to be 1, got %f", *metric.Gauge.Value)
	}
}

func TestSyncer_SyncFunction(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: []VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
			Query: "test_query",
		},
		PrometheusAPI: mockPrometheusAPI,
		DB:            testDB,
	}
	syncer.Init(t.Context())

	// Call the Sync function
	syncer.Sync(t.Context())

	// Verify the metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) == 0 {
		t.Error("expected metrics to be inserted, but found none")
	}
	if metrics[0].Name != "test_metric" {
		t.Errorf("expected metric name to be %s, got %s", "test_metric", metrics[0].Name)
	}
	if metrics[0].Value != 123.45 {
		t.Errorf("expected metric value to be %f, got %f", 123.45, metrics[0].Value)
	}
}

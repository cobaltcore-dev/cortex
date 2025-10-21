// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
)

// Mock metric implementation for testing
type mockMetric struct {
	name      string
	timestamp time.Time
	value     float64
	tableName string
}

func (m mockMetric) GetName() string              { return m.name }
func (m mockMetric) GetTimestamp() time.Time      { return m.timestamp }
func (m mockMetric) GetValue() float64            { return m.value }
func (m mockMetric) TableName() string            { return m.tableName }
func (m mockMetric) Indexes() map[string][]string { return nil }
func (m mockMetric) With(name string, t time.Time, v float64) prometheus.PrometheusMetric {
	return mockMetric{name: name, timestamp: t, value: v, tableName: m.tableName}
}

func TestNewTypedSyncer(t *testing.T) {
	ds := v1alpha1.Datasource{
		Spec: v1alpha1.DatasourceSpec{
			Prometheus: v1alpha1.PrometheusDatasource{
				Query:             "up",
				Alias:             "test_metric",
				TimeRangeSeconds:  3600,
				IntervalSeconds:   60,
				ResolutionSeconds: 15,
			},
		},
	}

	mockDB := &db.DB{}
	mockHTTPClient := &http.Client{}
	prometheusURL := "http://prometheus:9090"
	monitor := datasources.Monitor{}

	syncer := newTypedSyncer[mockMetric](ds, mockDB, mockHTTPClient, prometheusURL, monitor)

	if syncer == nil {
		t.Fatal("newTypedSyncer returned nil")
	}
}

func TestPrometheusTimelineData(t *testing.T) {
	start := time.Unix(1609459200, 0)
	end := time.Unix(1609459800, 0)
	duration := end.Sub(start)

	metrics := []mockMetric{
		{name: "test", timestamp: start, value: 1.0, tableName: "test_table"},
		{name: "test", timestamp: end, value: 2.0, tableName: "test_table"},
	}

	data := prometheusTimelineData[mockMetric]{
		Metrics:  metrics,
		Duration: duration,
		Start:    start,
		End:      end,
	}

	if len(data.Metrics) != 2 {
		t.Errorf("Expected 2 metrics, got %d", len(data.Metrics))
	}

	if data.Duration != duration {
		t.Errorf("Expected duration %v, got %v", duration, data.Duration)
	}

	if !data.Start.Equal(start) {
		t.Errorf("Expected start %v, got %v", start, data.Start)
	}

	if !data.End.Equal(end) {
		t.Errorf("Expected end %v, got %v", end, data.End)
	}
}

func TestPrometheusRangeMetric(t *testing.T) {
	jsonData := `{
		"metric": {},
		"values": [
			[1609459200, "0.5"],
			[1609459260, "0.7"]
		]
	}`

	var rangeMetric prometheusRangeMetric[mockMetric]
	err := json.Unmarshal([]byte(jsonData), &rangeMetric)
	if err != nil {
		t.Errorf("Failed to unmarshal JSON: %v", err)
	}

	if len(rangeMetric.Values) != 2 {
		t.Errorf("Expected 2 values, got %d", len(rangeMetric.Values))
	}

	// Test first value
	if len(rangeMetric.Values[0]) != 2 {
		t.Errorf("Expected value array of length 2, got %d", len(rangeMetric.Values[0]))
	}

	timestamp, ok := rangeMetric.Values[0][0].(float64)
	if !ok || timestamp != 1609459200 {
		t.Errorf("Expected timestamp 1609459200, got %v", rangeMetric.Values[0][0])
	}

	value, ok := rangeMetric.Values[0][1].(string)
	if !ok || value != "0.5" {
		t.Errorf("Expected value '0.5', got %v", rangeMetric.Values[0][1])
	}
}

func TestMockMetric(t *testing.T) {
	now := time.Now()

	m := mockMetric{
		name:      "test_metric",
		timestamp: now,
		value:     42.0,
		tableName: "test_table",
	}

	if m.GetName() != "test_metric" {
		t.Errorf("Expected name 'test_metric', got %s", m.GetName())
	}

	if !m.GetTimestamp().Equal(now) {
		t.Errorf("Expected timestamp %v, got %v", now, m.GetTimestamp())
	}

	if m.GetValue() != 42.0 {
		t.Errorf("Expected value 42.0, got %f", m.GetValue())
	}

	if m.TableName() != "test_table" {
		t.Errorf("Expected table name 'test_table', got %s", m.TableName())
	}

	if m.Indexes() != nil {
		t.Errorf("Expected nil indexes, got %v", m.Indexes())
	}

	// Test With method
	newTime := time.Unix(1609459200, 0)
	newMetric := m.With("new_name", newTime, 100.0)

	if newMetric.GetName() != "new_name" {
		t.Errorf("Expected new name 'new_name', got %s", newMetric.GetName())
	}

	if !newMetric.GetTimestamp().Equal(newTime) {
		t.Errorf("Expected new timestamp %v, got %v", newTime, newMetric.GetTimestamp())
	}

	if newMetric.GetValue() != 100.0 {
		t.Errorf("Expected new value 100.0, got %f", newMetric.GetValue())
	}
}

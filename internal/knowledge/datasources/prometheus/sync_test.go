// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Mock metric implementation for testing
type mockMetric struct {
	Name      string    `db:"name"`
	Timestamp time.Time `db:"timestamp"`
	Value     float64   `db:"value"`
}

func (m mockMetric) GetName() string              { return m.Name }
func (m mockMetric) GetTimestamp() time.Time      { return m.Timestamp }
func (m mockMetric) GetValue() float64            { return m.Value }
func (m mockMetric) TableName() string            { return "test_metrics" }
func (m mockMetric) Indexes() map[string][]string { return nil }
func (m mockMetric) With(name string, t time.Time, v float64) PrometheusMetric {
	return mockMetric{Name: name, Timestamp: t, Value: v}
}

func TestNewTypedSyncer(t *testing.T) {
	ds := v1alpha1.Datasource{
		Spec: v1alpha1.DatasourceSpec{
			Prometheus: v1alpha1.PrometheusDatasource{
				Query:      "up",
				Alias:      "test_metric",
				TimeRange:  metav1.Duration{Duration: 604800 * time.Second}, // 7 days
				Interval:   metav1.Duration{Duration: 60 * time.Second},
				Resolution: metav1.Duration{Duration: 15 * time.Second},
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

func TestSyncerFetch(t *testing.T) {
	tests := []struct {
		name           string
		prometheusResp string
		statusCode     int
		wantErr        bool
		expectedCount  int
	}{
		{
			name: "successful fetch with metrics",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{
							"metric": {},
							"values": [
								[1609459200, "0.5"],
								[1609459260, "0.7"]
							]
						}
					]
				}
			}`,
			statusCode:    200,
			expectedCount: 2,
		},
		{
			name: "empty result",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": []
				}
			}`,
			statusCode:    200,
			expectedCount: 0,
		},
		{
			name: "prometheus error status",
			prometheusResp: `{
				"status": "error",
				"errorType": "bad_data",
				"error": "invalid query"
			}`,
			statusCode: 200,
			wantErr:    true,
		},
		{
			name:           "http error",
			prometheusResp: "",
			statusCode:     500,
			wantErr:        true,
		},
		{
			name:           "invalid json",
			prometheusResp: "invalid json",
			statusCode:     200,
			wantErr:        true,
		},
		{
			name: "invalid value format",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{
							"metric": {},
							"values": [
								[1609459200, "invalid"]
							]
						}
					]
				}
			}`,
			statusCode: 200,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.statusCode != 200 {
					w.WriteHeader(tt.statusCode)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tt.prometheusResp)); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
			}))
			defer server.Close()

			s := &syncer[mockMetric]{
				host:                  server.URL,
				query:                 "up",
				alias:                 "test_metric",
				syncResolutionSeconds: 60,
				httpClient:            &http.Client{},
				monitor:               datasources.Monitor{},
				sleepBetweenRequests:  0,
			}

			start := time.Unix(1609459200, 0)
			end := time.Unix(1609459800, 0)

			data, err := s.fetch(start, end)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(data.Metrics) != tt.expectedCount {
				t.Errorf("expected %d metrics, got %d", tt.expectedCount, len(data.Metrics))
			}

			if !data.Start.Equal(start) {
				t.Errorf("expected start %v, got %v", start, data.Start)
			}

			if !data.End.Equal(end) {
				t.Errorf("expected end %v, got %v", end, data.End)
			}
		})
	}
}

func TestSyncerGetSyncWindowStart(t *testing.T) {
	tests := []struct {
		name           string
		setupDB        func(*db.DB)
		expectedResult func(time.Time) bool
		wantErr        bool
	}{
		{
			name: "empty database - returns time range in past",
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
			},
			expectedResult: func(result time.Time) bool {
				expectedStart := time.Now().Add(-24 * time.Hour)
				return result.Before(time.Now()) && result.After(expectedStart.Add(-time.Minute))
			},
		},
		{
			name: "existing metrics - returns latest timestamp",
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}

				metric := mockMetric{
					Name:      "test_metric",
					Timestamp: time.Unix(1609459200, 0),
					Value:     1.0,
				}

				if err := testDB.Insert(&metric); err != nil {
					t.Fatalf("failed to insert metric: %v", err)
				}
			},
			expectedResult: func(result time.Time) bool {
				expected := time.Unix(1609459200, 0)
				return result.Equal(expected)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			tt.setupDB(&testDB)

			s := &syncer[mockMetric]{
				db:                   &testDB,
				alias:                "test_metric",
				syncTimeRange:        24 * time.Hour,
				sleepBetweenRequests: 0,
			}

			result, err := s.getSyncWindowStart()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if !tt.expectedResult(result) {
				t.Errorf("result validation failed for time: %v", result)
			}
		})
	}
}

func TestSyncerSync(t *testing.T) {
	tests := []struct {
		name           string
		prometheusResp string
		setupDB        func(*db.DB)
		startTime      time.Time
		expectedCalls  int
	}{
		{
			name: "sync with metrics",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{
							"metric": {},
							"values": [
								[1609459200, "0.5"]
							]
						}
					]
				}
			}`,
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
			},
			startTime:     time.Unix(1609459200, 0),
			expectedCalls: 1,
		},
		{
			name: "sync future time - no calls",
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
			},
			startTime:     time.Now().Add(time.Hour),
			expectedCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			tt.setupDB(&testDB)

			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tt.prometheusResp)); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
			}))
			defer server.Close()

			s := &syncer[mockMetric]{
				db:                    &testDB,
				host:                  server.URL,
				query:                 "up",
				alias:                 "test_metric",
				syncTimeRange:         24 * time.Hour,
				syncInterval:          time.Hour,
				syncResolutionSeconds: 60,
				httpClient:            &http.Client{},
				monitor:               datasources.Monitor{},
				sleepBetweenRequests:  0,
			}

			s.sync(tt.startTime)

			if callCount < tt.expectedCalls {
				t.Errorf("expected at least %d HTTP calls, got %d", tt.expectedCalls, callCount)
			}
		})
	}
}

func TestSyncerSyncMethod(t *testing.T) {
	tests := []struct {
		name           string
		prometheusResp string
		setupDB        func(*db.DB)
		expectedCount  int64
		wantErr        bool
	}{
		{
			name: "successful full sync",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{
							"metric": {},
							"values": [
								[1609459200, "0.5"],
								[1609459260, "0.7"]
							]
						}
					]
				}
			}`,
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
			},
			expectedCount: 2,
		},
		{
			name: "empty prometheus response",
			prometheusResp: `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": []
				}
			}`,
			setupDB: func(testDB *db.DB) {
				err := testDB.CreateTable(testDB.AddTable(mockMetric{}))
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			tt.setupDB(&testDB)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify URL parameters
				if !strings.Contains(r.URL.RawQuery, "query=up") {
					t.Error("expected query parameter in URL")
				}
				if !strings.Contains(r.URL.RawQuery, "start=") {
					t.Error("expected start parameter in URL")
				}
				if !strings.Contains(r.URL.RawQuery, "end=") {
					t.Error("expected end parameter in URL")
				}
				if !strings.Contains(r.URL.RawQuery, "step=60") {
					t.Error("expected step parameter in URL")
				}

				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tt.prometheusResp)); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
			}))
			defer server.Close()

			s := &syncer[mockMetric]{
				db:                    &testDB,
				host:                  server.URL,
				query:                 "up",
				alias:                 "test_metric",
				syncTimeRange:         24 * time.Hour,
				syncInterval:          time.Hour,
				syncResolutionSeconds: 60,
				httpClient:            &http.Client{},
				monitor:               datasources.Monitor{},
				sleepBetweenRequests:  0,
			}

			nResults, nextSync, err := s.Sync(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if nResults != tt.expectedCount {
				t.Errorf("expected %d results, got %d", tt.expectedCount, nResults)
			}

			if nextSync.Before(time.Now()) {
				t.Error("next sync time should be in the future")
			}
		})
	}
}

func TestPrometheusTimelineData(t *testing.T) {
	start := time.Unix(1609459200, 0)
	end := time.Unix(1609459800, 0)
	duration := end.Sub(start)

	metrics := []mockMetric{
		{Name: "test", Timestamp: start, Value: 1.0},
		{Name: "test", Timestamp: end, Value: 2.0},
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
		Name:      "test_metric",
		Timestamp: now,
		Value:     42.0,
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

	if m.TableName() != "test_metrics" {
		t.Errorf("Expected table name 'test_metrics', got %s", m.TableName())
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

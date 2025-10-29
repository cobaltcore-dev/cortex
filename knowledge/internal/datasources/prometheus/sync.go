// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
)

type typedSyncer interface {
	Sync(context.Context) (nResults int64, nextSync time.Time, err error)
}

// Create a new prometheus metric syncer with a connected database
// and authenticated HTTP client.
func newTypedSyncer[M prometheus.PrometheusMetric](
	ds v1alpha1.Datasource,
	db *db.DB,
	httpClient *http.Client,
	prometheusURL string,
	monitor datasources.Monitor,
) typedSyncer {
	return &syncer[M]{
		db:                    db,
		httpClient:            httpClient,
		host:                  prometheusURL,
		monitor:               monitor,
		query:                 ds.Spec.Prometheus.Query,
		alias:                 ds.Spec.Prometheus.Alias,
		syncTimeRange:         ds.Spec.Prometheus.TimeRange.Duration,
		syncInterval:          ds.Spec.Prometheus.Interval.Duration,
		syncResolutionSeconds: int(ds.Spec.Prometheus.Resolution.Duration.Seconds()),
		sleepBetweenRequests:  500 * time.Millisecond,
	}
}

// Prometheus syncer for an arbitrary prometheus metric model.
type syncer[M prometheus.PrometheusMetric] struct {
	// Host from which to fetch the metrics.
	host string
	// Prometheus query to execute.
	query string
	// Metric alias under which to store the metrics.
	alias string

	// Monitor to track the syncer.
	monitor datasources.Monitor

	// Time range to sync in each operation.
	syncTimeRange time.Duration
	// Sync interval to split the sync into smaller chunks.
	syncInterval time.Duration
	// Sync resolution for the Prometheus query.
	syncResolutionSeconds int

	// Database connected from credentials provided in the datasource config.
	db *db.DB
	// Authenticated HTTP client to connect to Prometheus.
	httpClient *http.Client

	// How long to sleep between requests to avoid overloading the Prometheus server.
	// A default jitter will be applied to this duration.
	sleepBetweenRequests time.Duration
}

// Metrics fetched from Prometheus with the time window
// and resolution specified in the query.
type prometheusTimelineData[M prometheus.PrometheusMetric] struct {
	Metrics  []M
	Duration time.Duration
	Start    time.Time
	End      time.Time
}

// Prometheus range metric returned by the query_range API.
type prometheusRangeMetric[M prometheus.PrometheusMetric] struct {
	Metric M       `json:"metric"`
	Values [][]any `json:"values"`
}

// Fetch metrics from Prometheus. The query is executed in the time window
// [start, end] with the specified resolution.
func (s *syncer[M]) fetch(start time.Time, end time.Time) (*prometheusTimelineData[M], error) {
	if s.monitor.RequestTimer != nil {
		hist := s.monitor.RequestTimer.WithLabelValues(
			"prometheus_" + s.alias,
		)
		timer := prometheusclient.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
	urlStr := s.host + "/api/v1/query_range"
	urlStr += "?query=" + url.QueryEscape(s.query)
	urlStr += "&start=" + strconv.FormatInt(start.Unix(), 10)
	urlStr += "&end=" + strconv.FormatInt(end.Unix(), 10)
	urlStr += "&step=" + strconv.Itoa(s.syncResolutionSeconds)
	slog.Info("fetching metrics from", "url", urlStr)

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	var prometheusData struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	// Copy the body to print it out in case of an error.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore the body for further processing.

	err = json.NewDecoder(resp.Body).Decode(&prometheusData)
	if err != nil {
		bodyTrunc := string(bodyBytes)
		if len(bodyTrunc) > 100 {
			bodyTrunc = bodyTrunc[:100] + "..."
		}
		slog.Error(
			"failed to decode response",
			"body", bodyTrunc,
			"error", err,
		)
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if prometheusData.Status != "success" {
		return nil, fmt.Errorf("failed to fetch metrics: %s", prometheusData.Status)
	}

	// Decode the Result as a prometheusRangeMetric. Set the timestamp and value
	// to default values. Afterward, unwrap the metrics and set the timestamp and value.
	var flatMetrics []M
	for _, rawMetric := range prometheusData.Data.Result {
		var rangeMetric prometheusRangeMetric[M]
		if err := json.Unmarshal(rawMetric, &rangeMetric); err != nil {
			return nil, fmt.Errorf("failed to unmarshal range metric: %w", err)
		}

		for _, value := range rangeMetric.Values {
			if len(value) != 2 {
				return nil, fmt.Errorf("invalid value: %v", value)
			}
			valTimeFloat, ok := value[0].(float64)
			if !ok {
				return nil, fmt.Errorf("invalid timestamp: %v", value[0])
			}
			valTime := time.Unix(int64(valTimeFloat), 0)
			valContent, err := strconv.ParseFloat(value[1].(string), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %v", value[1])
			}

			metric := rangeMetric.Metric.With(s.alias, valTime, valContent)
			flatMetrics = append(flatMetrics, metric.(M))
		}
	}

	if s.monitor.RequestProcessedCounter != nil {
		s.monitor.RequestProcessedCounter.WithLabelValues(
			"prometheus_" + s.alias,
		).Inc()
	}
	return &prometheusTimelineData[M]{
		Metrics:  flatMetrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

// Get the start of the sync window for the given metric.
// The start window is either 4 weeks in the past or the
// latest metrics timestamp in the database.
func (s *syncer[M]) getSyncWindowStart() (time.Time, error) {
	// Check if there are any metrics in the database.
	var model M
	tableName := model.TableName()
	nRows, err := s.db.SelectInt(
		"SELECT COUNT(*) FROM "+tableName+" WHERE name = :name",
		map[string]any{"name": s.alias},
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to count rows: %w", err)
	}
	slog.Debug("number of rows", "nRows", nRows, "tableName", tableName)
	if nRows == 0 {
		// No metrics in the database yet. Start <timeRange> in the past.
		start := time.Now().Add(-s.syncTimeRange)
		return start, nil
	}
	if err := s.db.SelectOne(
		&model, `
		 SELECT name, timestamp FROM `+tableName+`
		  WHERE name = :name
		  ORDER BY timestamp
		   DESC LIMIT 1
		`,
		map[string]any{"name": s.alias},
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
	end := start.Add(s.syncInterval)
	if start.After(time.Now()) || end.After(time.Now()) {
		return // Finished syncing.
	}

	var model M
	tableName := model.TableName()
	slog.Info(
		"syncing Prometheus data", "metricAlias", s.alias,
		"start", start, "end", end, "tableName", tableName,
	)
	// Drop all metrics that are older than <timeRangeSeconds> from the config file. (Default is 4 weeks)
	result, err := s.db.Exec(
		"DELETE FROM "+tableName+" WHERE name = :name AND timestamp < :timestamp",
		map[string]any{"name": s.alias, "timestamp": time.Now().Add(-s.syncTimeRange)},
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
	prometheusData, err := s.fetch(start, end)
	if err != nil {
		slog.Error("failed to fetch metrics", "error", err)
		return
	}
	if err := db.BulkInsert(s.db, *s.db, prometheusData.Metrics...); err != nil {
		slog.Error("failed to bulk insert metrics", "error", err)
		return
	}
	slog.Info(
		"synced Prometheus data", "newMetrics", len(prometheusData.Metrics),
		"metricAlias", s.alias, "start", start, "end", end,
	)

	// Don't overload the Prometheus server.
	time.Sleep(jobloop.DefaultJitter(s.sleepBetweenRequests))
	// Continue syncing.
	s.sync(end)
}

// Sync the Prometheus metrics with the database.
func (s *syncer[M]) Sync(context.Context) (nResults int64, nextSync time.Time, err error) {
	var model M
	if err := s.db.CreateTable(s.db.AddTable(model)); err != nil {
		return 0, time.Time{}, err
	}

	slog.Info("syncing metrics", "metricAlias", s.alias)
	// Sync this metric until we are caught up.
	start, err := s.getSyncWindowStart()
	if err != nil {
		slog.Error("failed to get sync window start", "error", err)
		return 0, time.Time{}, err
	}
	s.sync(start)
	slog.Info("synced metrics", "metricAlias", s.alias)

	nResults, err = s.db.SelectInt(
		"SELECT COUNT(*) FROM "+model.TableName()+" WHERE name = :name",
		map[string]any{"name": s.alias},
	)
	if err != nil {
		return 0, time.Time{}, err
	}
	if s.monitor.ObjectsGauge != nil {
		s.monitor.ObjectsGauge.
			WithLabelValues("prometheus_" + s.alias).
			Set(float64(nResults))
	}
	nextSync = time.Now().Add(s.syncInterval)
	return nResults, nextSync, nil
}

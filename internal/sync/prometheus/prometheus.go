// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics fetched from Prometheus with the time window
// and resolution specified in the query.
type prometheusTimelineData[M PrometheusMetric] struct {
	Metrics  []M
	Duration time.Duration
	Start    time.Time
	End      time.Time
}

// Prometheus range metric returned by the query_range API.
type prometheusRangeMetric[M PrometheusMetric] struct {
	Metric M       `json:"metric"`
	Values [][]any `json:"values"`
}

// Prometheus API to fetch metrics from Prometheus.
type PrometheusAPI[M PrometheusMetric] interface {
	FetchMetrics(
		query string,
		start time.Time,
		end time.Time,
		resolutionSeconds int,
	) (*prometheusTimelineData[M], error)
}

// Prometheus API implementation.
type prometheusAPI[M PrometheusMetric] struct {
	// Prometheus host from which to fetch metrics.
	hostConf conf.SyncPrometheusHostConfig
	// Prometheus metric to fetch.
	metricConf conf.SyncPrometheusMetricConfig
	// Monitor to observe the API.
	monitor sync.Monitor
}

// Create a new Prometheus API with the given Prometheus metric type.
func NewPrometheusAPI[M PrometheusMetric](
	hostConf conf.SyncPrometheusHostConfig,
	metricConf conf.SyncPrometheusMetricConfig,
	monitor sync.Monitor,
) PrometheusAPI[M] {

	return &prometheusAPI[M]{
		hostConf:   hostConf,
		metricConf: metricConf,
		monitor:    monitor,
	}
}

// Fetch VMware vROps metrics from Prometheus.
// The query is executed in the time window [start, end] with the specified resolution.
func (api *prometheusAPI[M]) FetchMetrics(
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*prometheusTimelineData[M], error) {

	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues(
			"prometheus_" + api.metricConf.Alias,
		)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
	urlStr := api.hostConf.URL + "/api/v1/query_range"
	urlStr += "?query=" + url.QueryEscape(query)
	urlStr += "&start=" + strconv.FormatInt(start.Unix(), 10)
	urlStr += "&end=" + strconv.FormatInt(end.Unix(), 10)
	urlStr += "&step=" + strconv.Itoa(resolutionSeconds)
	slog.Info("fetching metrics from", "url", urlStr)

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client, err := sync.NewHTTPClient(api.hostConf.SSO)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := client.Do(req)
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
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore the body for further processing

	err = json.NewDecoder(resp.Body).Decode(&prometheusData)
	if err != nil {
		slog.Error(
			"failed to decode response",
			"body", string(bodyBytes),
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

			metric := rangeMetric.Metric.With(api.metricConf.Alias, valTime, valContent)
			flatMetrics = append(flatMetrics, metric.(M))
		}
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues(
			"prometheus_" + api.metricConf.Alias,
		).Inc()
	}
	return &prometheusTimelineData[M]{
		Metrics:  flatMetrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

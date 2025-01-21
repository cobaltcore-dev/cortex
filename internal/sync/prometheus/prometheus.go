// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// One metric datapoint in the Prometheus timeline.
type PrometheusMetric interface {
	// Table name into which the metric should be stored.
	GetTableName() string
	// Name under which the metric is stored in Prometheus.
	GetName() string
	// Value of this metric datapoint.
	GetValue() float64
	// Set the time of this metric datapoint.
	SetTimestamp(time time.Time)
	// Set the value of this metric datapoint.
	SetValue(value float64)
}

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

type prometheusAPI[M PrometheusMetric] struct {
	Secrets conf.SecretPrometheusConfig
}

// Create a new Prometheus API with the given Prometheus metric type.
func NewPrometheusAPI[M PrometheusMetric]() PrometheusAPI[M] {
	return &prometheusAPI[M]{
		Secrets: conf.NewSecretConfig().SecretPrometheusConfig,
	}
}

// Fetch VMware vROps metrics from Prometheus.
// The query is executed in the time window [start, end] with the
// specified resolution. Note: the query is not URLencoded atm. (TODO)
func (api *prometheusAPI[M]) FetchMetrics(
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*prometheusTimelineData[M], error) {
	// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
	url := api.Secrets.PrometheusURL + "/api/v1/query_range"
	url += "?query=" + query
	url += "&start=" + strconv.FormatInt(start.Unix(), 10)
	url += "&end=" + strconv.FormatInt(end.Unix(), 10)
	url += "&step=" + strconv.Itoa(resolutionSeconds)
	logging.Log.Info("fetching metrics from", "url", url)

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
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
	err = json.NewDecoder(resp.Body).Decode(&prometheusData)
	if err != nil {
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

			metric := rangeMetric.Metric
			metric.SetTimestamp(valTime)
			metric.SetValue(valContent)
			flatMetrics = append(flatMetrics, metric)
		}
	}

	return &prometheusTimelineData[M]{
		Metrics:  flatMetrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

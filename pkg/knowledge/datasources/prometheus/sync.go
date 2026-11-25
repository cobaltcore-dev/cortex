// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	prometheusclient "github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("prometheus")

type typedSyncer interface {
	Sync(context.Context) (nResults int64, nextSync time.Time, err error)
}

// Create a new prometheus metric syncer with a connected database
// and authenticated HTTP client.
func newTypedSyncer[M PrometheusMetric](
	ds v1alpha1.Datasource,
	st store.Store,
	httpClient *http.Client,
	prometheusURL string,
	monitor datasources.Monitor,
) typedSyncer {

	return &syncer[M]{
		store:                 st,
		httpClient:            httpClient,
		host:                  prometheusURL,
		monitor:               monitor,
		query:                 ds.Spec.Prometheus.Query,
		alias:                 ds.Spec.Prometheus.Alias,
		syncTimeRange:         ds.Spec.Prometheus.TimeRange.Duration,
		syncInterval:          ds.Spec.Prometheus.Interval.Duration,
		syncResolutionSeconds: int(ds.Spec.Prometheus.Resolution.Seconds()),
		sleepBetweenRequests:  500 * time.Millisecond,
	}
}

// Prometheus syncer for an arbitrary prometheus metric model.
type syncer[M PrometheusMetric] struct {
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

	// Store in which the metrics are persisted, partitioned by alias.
	store store.Store
	// Authenticated HTTP client to connect to Prometheus.
	httpClient *http.Client

	// How long to sleep between requests to avoid overloading the Prometheus server.
	// A default jitter will be applied to this duration.
	sleepBetweenRequests time.Duration
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

// Fetch metrics from Prometheus. The query is executed in the time window
// [start, end] with the specified resolution.
func (s *syncer[M]) fetch(start, end time.Time) (*prometheusTimelineData[M], error) {
	if s.monitor.RequestTimer != nil {
		hist := s.monitor.RequestTimer.WithLabelValues(
			"prometheus_" + s.alias,
		)
		timer := prometheusclient.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// See https://io/docs/prometheus/latest/querying/api/#range-queries
	urlStr := s.host + "/api/v1/query_range"
	urlStr += "?query=" + url.QueryEscape(s.query)
	urlStr += "&start=" + strconv.FormatInt(start.Unix(), 10)
	urlStr += "&end=" + strconv.FormatInt(end.Unix(), 10)
	urlStr += "&step=" + strconv.Itoa(s.syncResolutionSeconds)
	log.V(1).Info("fetching metrics from", "url", urlStr)

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
		log.Error(
			err,
			"failed to decode response",
			"body", bodyTrunc,
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

// Sync the Prometheus metrics into the store.
//
// The sync is done in intervals, starting at the latest stored metric or
// <timeRange> in the past if there are no metrics yet, until we are caught up
// with the current time. Metrics outside of the time range are dropped.
func (s *syncer[M]) Sync(ctx context.Context) (nResults int64, nextSync time.Time, err error) {
	existing, err := store.ListAll[M](ctx, s.store, s.alias)
	stored := !errors.Is(err, store.ErrNotFound)
	if err != nil && stored {
		return 0, time.Time{}, fmt.Errorf("failed to list stored metrics: %w", err)
	}
	start := time.Now().Add(-s.syncTimeRange)
	if len(existing) > 0 {
		var latest time.Time
		for _, m := range existing {
			if m.GetTimestamp().After(latest) {
				latest = m.GetTimestamp()
			}
		}
		if latest.IsZero() {
			return 0, time.Time{}, errors.New("latest timestamp is zero")
		}
		start = latest
	}

	log.V(1).Info("syncing metrics", "metricAlias", s.alias, "start", start)
	var fetched []M
	// Sync full intervals only.
	for end := start.Add(s.syncInterval); !end.After(time.Now()); start, end = end, end.Add(s.syncInterval) {
		data, err := s.fetch(start, end)
		if err != nil {
			// Keep what we fetched so far and continue with the next sync.
			log.Error(err, "failed to fetch metrics", "metricAlias", s.alias, "start", start, "end", end)
			break
		}
		fetched = append(fetched, data.Metrics...)
		log.V(1).Info(
			"fetched Prometheus data", "newMetrics", len(data.Metrics),
			"metricAlias", s.alias, "start", start, "end", end,
		)
		// Don't overload the Prometheus server.
		time.Sleep(jobloop.DefaultJitter(s.sleepBetweenRequests))
	}

	// Drop all metrics that are older than <timeRange> from the datasource spec.
	cutoff := time.Now().Add(-s.syncTimeRange)
	metrics := make([]M, 0, len(existing)+len(fetched))
	for _, m := range slices.Concat(existing, fetched) {
		if !m.GetTimestamp().Before(cutoff) {
			metrics = append(metrics, m)
		}
	}
	nDropped := len(existing) + len(fetched) - len(metrics)
	// Also write if nothing changed but the metrics were not stored yet, so
	// that readers can tell a synced but empty datasource from a missing one.
	if len(fetched) > 0 || nDropped > 0 || !stored {
		if err := store.ReplaceAll(ctx, s.store, s.alias, metrics); err != nil {
			return 0, time.Time{}, fmt.Errorf("failed to store metrics: %w", err)
		}
	}
	log.Info("synced metrics", "metricAlias", s.alias, "new", len(fetched), "dropped", nDropped)

	nResults = int64(len(metrics))
	if s.monitor.ObjectsGauge != nil {
		s.monitor.ObjectsGauge.
			WithLabelValues("prometheus_" + s.alias).
			Set(float64(nResults))
	}
	nextSync = time.Now().Add(s.syncInterval)
	return nResults, nextSync, nil
}

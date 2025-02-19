// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

func TestFetchMetrics(t *testing.T) {
	// Mock the Prometheus API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query_range" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			//nolint:errcheck
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []map[string]interface{}{
						{
							"metric": map[string]interface{}{
								"cluster":        "test_cluster",
								"cluster_type":   "test_cluster_type",
								"collector":      "test_collector",
								"datacenter":     "test_datacenter",
								"hostsystem":     "test_hostsystem",
								"instance_uuid":  "test_instance_uuid",
								"internal_name":  "test_internal_name",
								"job":            "test_job",
								"project":        "test_project",
								"prometheus":     "test_prometheus",
								"region":         "test_region",
								"vccluster":      "test_vccluster",
								"vcenter":        "test_vcenter",
								"virtualmachine": "test_virtualmachine",
							},
							"values": [][]interface{}{
								{float64(time.Now().Unix()), "123.45"},
							},
						},
					},
				},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	start := time.Now().Add(-time.Hour)
	end := time.Now()
	resolutionSeconds := 60

	api := &prometheusAPI[VROpsVMMetric]{
		hostConf: conf.SyncPrometheusHostConfig{
			URL: server.URL,
		},
		metricConf: conf.SyncPrometheusMetricConfig{
			Alias: "test_metric",
		},
	}
	data, err := api.FetchMetrics("test_query", start, end, resolutionSeconds)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the results
	if len(data.Metrics) != 1 {
		t.Errorf("expected 1 metric, got %d", len(data.Metrics))
	}
	metric := data.Metrics[0]
	if metric.Name != "test_metric" {
		t.Errorf("expected metric name to be %s, got %s", "test_metric", metric.Name)
	}
	if metric.Value != 123.45 {
		t.Errorf("expected value to be %f, got %f", 123.45, metric.Value)
	}
}

func TestFetchMetricsFailure(t *testing.T) {
	// Mock the Prometheus API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	start := time.Now().Add(-time.Hour)
	end := time.Now()
	resolutionSeconds := 60

	api := &prometheusAPI[*VROpsVMMetric]{
		hostConf: conf.SyncPrometheusHostConfig{
			URL: server.URL,
		},
	}
	_, err := api.FetchMetrics("test_query", start, end, resolutionSeconds)
	if err == nil {
		t.Fatalf("expected error, got none")
	}
}

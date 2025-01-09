// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
type PrometheusMetric struct {
	Metric struct {
		Name           string `json:"__name__"`
		Cluster        string `json:"cluster"`
		ClusterType    string `json:"cluster_type"`
		Collector      string `json:"collector"`
		Datacenter     string `json:"datacenter"`
		HostSystem     string `json:"hostsystem"`
		InstanceUUID   string `json:"instance_uuid"`
		InternalName   string `json:"internal_name"`
		Job            string `json:"job"`
		Project        string `json:"project"`
		Prometheus     string `json:"prometheus"`
		Region         string `json:"region"`
		VCCluster      string `json:"vccluster"`
		VCenter        string `json:"vcenter"`
		VirtualMachine string `json:"virtualmachine"`
	} `json:"metric"`
	Values [][]interface{} `json:"values"`
}

type PrometheusTimelineData struct {
	Metrics  []PrometheusMetric `json:"metrics"`
	Duration time.Duration      `json:"duration"`
	Start    time.Time          `json:"start"`
	End      time.Time          `json:"end"`
}

func fetchMetrics(
	prometheusURL string,
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*PrometheusTimelineData, error) {
	url := fmt.Sprintf("%s/api/v1/query_range", prometheusURL)
	url = fmt.Sprintf("%s?query=%s", url, query)
	url = fmt.Sprintf("%s&start=%d", url, start.Unix())
	url = fmt.Sprintf("%s&end=%d", url, end.Unix())
	url = fmt.Sprintf("%s&step=%d", url, resolutionSeconds)
	log.Printf("Fetching metrics from %s", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
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
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	if prometheusData.Status != "success" {
		return nil, fmt.Errorf("failed to fetch metrics: %s", prometheusData.Status)
	}

	metrics := make([]PrometheusMetric, 0, len(prometheusData.Data.Result))
	for _, raw := range prometheusData.Data.Result {
		var metric PrometheusMetric
		err = json.Unmarshal(raw, &metric)
		if err != nil {
			return nil, fmt.Errorf("failed to decode metric: %v", err)
		}
		metrics = append(metrics, metric)
	}

	return &PrometheusTimelineData{
		Metrics:  metrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

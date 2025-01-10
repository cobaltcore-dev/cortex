// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

type PrometheusMetric struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName      struct{}  `pg:"metrics"`
	Name           string    `json:"__name__" pg:"name"`
	Cluster        string    `json:"cluster" pg:"cluster"`
	ClusterType    string    `json:"cluster_type" pg:"cluster_type"`
	Collector      string    `json:"collector" pg:"collector"`
	Datacenter     string    `json:"datacenter" pg:"datacenter"`
	HostSystem     string    `json:"hostsystem" pg:"hostsystem"`
	InstanceUUID   string    `json:"instance_uuid" pg:"instance_uuid"`
	InternalName   string    `json:"internal_name" pg:"internal_name"`
	Job            string    `json:"job" pg:"job"`
	Project        string    `json:"project" pg:"project"`
	Prometheus     string    `json:"prometheus" pg:"prometheus"`
	Region         string    `json:"region" pg:"region"`
	VCCluster      string    `json:"vccluster" pg:"vccluster"`
	VCenter        string    `json:"vcenter" pg:"vcenter"`
	VirtualMachine string    `json:"virtualmachine" pg:"virtualmachine"`
	Timestamp      time.Time `json:"timestamp" pg:"timestamp"`
	Value          float64   `json:"value" pg:"value"`
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
	// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
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

	type rangeMetric struct {
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
	metrics := make([]rangeMetric, 0, len(prometheusData.Data.Result))
	for _, raw := range prometheusData.Data.Result {
		var metric rangeMetric
		err = json.Unmarshal(raw, &metric)
		if err != nil {
			return nil, fmt.Errorf("failed to decode metric: %v", err)
		}
		metrics = append(metrics, metric)
	}

	// Flatten the metrics
	var flatMetrics []PrometheusMetric
	for _, metric := range metrics {
		for _, value := range metric.Values {
			if len(value) != 2 {
				return nil, fmt.Errorf("invalid value: %v", value)
			}
			valTime, ok := value[0].(float64)
			if !ok {
				return nil, fmt.Errorf("invalid timestamp: %v", value[0])
			}
			// Parse string as float64 using strconv.ParseFloat
			valContent, err := strconv.ParseFloat(value[1].(string), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %v", value[1])
			}
			flatMetrics = append(flatMetrics, PrometheusMetric{
				Name:           metric.Metric.Name,
				Cluster:        metric.Metric.Cluster,
				ClusterType:    metric.Metric.ClusterType,
				Collector:      metric.Metric.Collector,
				Datacenter:     metric.Metric.Datacenter,
				HostSystem:     metric.Metric.HostSystem,
				InstanceUUID:   metric.Metric.InstanceUUID,
				InternalName:   metric.Metric.InternalName,
				Job:            metric.Metric.Job,
				Project:        metric.Metric.Project,
				Prometheus:     metric.Metric.Prometheus,
				Region:         metric.Metric.Region,
				VCCluster:      metric.Metric.VCCluster,
				VCenter:        metric.Metric.VCenter,
				VirtualMachine: metric.Metric.VirtualMachine,
				Timestamp:      time.Unix(int64(valTime), 0),
				Value:          valContent,
			})
		}
	}

	return &PrometheusTimelineData{
		Metrics:  flatMetrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

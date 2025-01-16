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

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// PrometheusMetric represents a single metric value from Prometheus
// that was generated the VMware vROps exporter.
// See: https://github.com/sapcc/vrops-exporter
type PrometheusMetric struct {
	//lint:ignore U1000 Field is used by the ORM.
	tableName struct{} `pg:"metrics"`
	// The name of the metric.
	Name string `json:"__name__" pg:"name"`
	// Kubernetes cluster name in which the metrics exporter is running.
	Cluster string `json:"cluster" pg:"cluster"`
	// Kubernetes cluster type in which the metrics exporter is running.
	ClusterType string `json:"cluster_type" pg:"cluster_type"`
	// The name of the metrics collector.
	Collector string `json:"collector" pg:"collector"`
	// Datacenter / availability zone of the virtual machine.
	Datacenter string `json:"datacenter" pg:"datacenter"`
	// Host system of the virtual machine.
	// Note: this value does not necessarily correspond to the
	// hypervisor service host contained in OpenStack.
	HostSystem string `json:"hostsystem" pg:"hostsystem"`
	// OpenStack UUID of the virtual machine instance.
	// Note: not all instances may be seen in the current OpenStack environment.
	InstanceUUID string `json:"instance_uuid" pg:"instance_uuid"`
	// Internal name of the virtual machine.
	InternalName string `json:"internal_name" pg:"internal_name"`
	// Exporter job name (usually "vrops-exporter").
	Job string `json:"job" pg:"job"`
	// OpenStack project ID of the virtual machine.
	Project string `json:"project" pg:"project"`
	// Prometheus instance from which the metric was fetched.
	Prometheus string `json:"prometheus" pg:"prometheus"`
	// Datacenter region (one level above availability zone).
	Region string `json:"region" pg:"region"`
	// VMware vCenter cluster name in which the virtual machine is running.
	VCCluster string `json:"vccluster" pg:"vccluster"`
	// VMware vCenter name in which the virtual machine is running.
	VCenter string `json:"vcenter" pg:"vcenter"`
	// Name of the virtual machine specified by the OpenStack user.
	VirtualMachine string `json:"virtualmachine" pg:"virtualmachine"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" pg:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" pg:"value"`
}

// Metrics fetched from Prometheus with the time window
// and resolution specified in the query.
type prometheusTimelineData struct {
	Metrics  []PrometheusMetric `json:"metrics"`
	Duration time.Duration      `json:"duration"`
	Start    time.Time          `json:"start"`
	End      time.Time          `json:"end"`
}

type PrometheusAPI interface {
	fetchMetrics(
		query string,
		start time.Time,
		end time.Time,
		resolutionSeconds int,
	) (*prometheusTimelineData, error)
}

type prometheusAPI struct {
	Conf PrometheusConfig
}

func NewPrometheusAPI() PrometheusAPI {
	return &prometheusAPI{
		Conf: NewPrometheusConfig(),
	}
}

// Fetch VMware vROps metrics from Prometheus.
// The query is executed in the time window [start, end] with the
// specified resolution. Note: the query is not URLencoded atm. (TODO)
func (api *prometheusAPI) fetchMetrics(
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*prometheusTimelineData, error) {
	// See https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
	url := api.Conf.GetPrometheusURL() + "/api/v1/query_range"
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
		Values [][]any `json:"values"`
	}
	metrics := make([]rangeMetric, 0, len(prometheusData.Data.Result))
	for _, raw := range prometheusData.Data.Result {
		var metric rangeMetric
		err = json.Unmarshal(raw, &metric)
		if err != nil {
			return nil, fmt.Errorf("failed to decode metric: %w", err)
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

	return &prometheusTimelineData{
		Metrics:  flatMetrics,
		Duration: end.Sub(start),
		Start:    start,
		End:      end,
	}, nil
}

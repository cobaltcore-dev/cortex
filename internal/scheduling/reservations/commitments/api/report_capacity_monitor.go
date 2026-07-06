// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

type ReportCapacityAPIMonitor struct {
	requestCounter   *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	reportedCapacity *prometheus.GaugeVec
}

func NewReportCapacityAPIMonitor() ReportCapacityAPIMonitor {
	m := ReportCapacityAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_capacity_api_requests_total",
			Help: "Total number of committed resource capacity API requests by HTTP status code",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_capacity_api_request_duration_seconds",
			Help:    "Duration of committed resource capacity API requests in seconds by HTTP status code",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"status_code"}),
		reportedCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_reported_capacity",
			Help: "Last reported capacity per resource and AZ as returned by the capacity API. unit_size indicates the GiB value of one declared unit (e.g. 480 for a HANA slot, 1 for variable-ratio RAM or cores).",
		}, []string{"resource", "az", "unit_size"}),
	}

	for _, statusCode := range []string{"200", "500", "503"} {
		m.requestCounter.WithLabelValues(statusCode)
		m.requestDuration.WithLabelValues(statusCode)
	}

	return m
}

func (m *ReportCapacityAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
	m.reportedCapacity.Describe(ch)
}

func (m *ReportCapacityAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
	m.reportedCapacity.Collect(ch)
}

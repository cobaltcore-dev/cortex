// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import "github.com/prometheus/client_golang/prometheus"

type QuotaAPIMonitor struct {
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func NewQuotaAPIMonitor() QuotaAPIMonitor {
	m := QuotaAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_quota_api_requests_total",
			Help: "Total number of quota API requests by status code.",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_quota_api_request_duration_seconds",
			Help:    "Duration of quota API requests in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"status_code"}),
	}
	for _, statusCode := range []string{"204", "400", "405", "500", "503"} {
		m.requestCounter.WithLabelValues(statusCode)
		m.requestDuration.WithLabelValues(statusCode)
	}
	return m
}

func (m *QuotaAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
}

func (m *QuotaAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
}

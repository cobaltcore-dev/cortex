// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package valkey

import "github.com/prometheus/client_golang/prometheus"

type monitor struct {
	connectionAttempts *prometheus.CounterVec
	duration           *prometheus.HistogramVec
	objects            *prometheus.CounterVec
	cache              *prometheus.CounterVec
}

// Monitor exposes metrics of the valkey stores.
var Monitor = monitor{
	connectionAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_valkey_connection_attempts_total",
		Help: "Total number of valkey connection attempts.",
	}, []string{"addr"}),
	duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_valkey_operation_duration_seconds",
		Help:    "Duration of store operations on valkey.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation", "scope"}),
	objects: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_valkey_objects_total",
		Help: "Total number of objects read from or written to valkey.",
	}, []string{"direction", "scope"}),
	cache: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_valkey_list_cache_total",
		Help: "Lists served from the in-process cache (hit) or read from valkey (miss).",
	}, []string{"scope", "result"}),
}

func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	m.connectionAttempts.Describe(ch)
	m.duration.Describe(ch)
	m.objects.Describe(ch)
	m.cache.Describe(ch)
}

func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.connectionAttempts.Collect(ch)
	m.duration.Collect(ch)
	m.objects.Collect(ch)
	m.cache.Collect(ch)
}

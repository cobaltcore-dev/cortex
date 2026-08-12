// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Monitor is the metrics sink for the CachingClient. It is optional on the
// client: a nil Monitor causes recording to be skipped entirely. It embeds
// prometheus.Collector so a concrete implementation can be registered with a
// Prometheus registry.
type Monitor interface {
	prometheus.Collector

	// observe records the current overlay size (live + tombstones) for a GVK.
	// It is called by the client while holding c.mu so size is consistent.
	observe(gvk schema.GroupVersionKind, size int)
}

// monitor is the default Prometheus-backed Monitor implementation.
//
// Overlay entries are normally evicted within milliseconds once the informer
// observes the change, so a plain gauge scraped by Prometheus would almost
// always read 0 and give no signal about transient spikes. To make abnormal
// growth visible even when the overlay is empty at scrape time, the monitor
// tracks a per-GVK high-watermark (the max size seen since the last scrape)
// which is reset on each Collect, plus a current-size gauge to confirm the
// overlay drains correctly.
type monitor struct {
	mu sync.Mutex
	// maxSinceScrape is the high-watermark of overlay size per GVK since the
	// last scrape. It is reset to a fresh map on each Collect.
	maxSinceScrape map[schema.GroupVersionKind]int
	// current is the last observed overlay size per GVK. It is a snapshot, not
	// reset on scrape.
	current map[schema.GroupVersionKind]int

	maxDesc     *prometheus.Desc
	currentDesc *prometheus.Desc
}

// NewMonitor creates a new Prometheus-backed CachingClient monitor. The prefix
// is prepended to every metric name (e.g. pass "cortex_" to produce
// "cortex_clientcache_overlay_entries_max").
func NewMonitor(prefix string) Monitor {
	return &monitor{
		maxSinceScrape: make(map[schema.GroupVersionKind]int),
		current:        make(map[schema.GroupVersionKind]int),
		maxDesc: prometheus.NewDesc(
			prefix+"clientcache_overlay_entries_max",
			"Maximum overlay entries (live + tombstones) per GVK since the last scrape",
			[]string{"gvk"}, nil,
		),
		currentDesc: prometheus.NewDesc(
			prefix+"clientcache_overlay_entries",
			"Current overlay entries (live + tombstones) per GVK at scrape time",
			[]string{"gvk"}, nil,
		),
	}
}

// observe records the current overlay size for a GVK, updating the
// high-watermark if it grew.
func (m *monitor) observe(gvk schema.GroupVersionKind, size int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current[gvk] = size
	if size > m.maxSinceScrape[gvk] {
		m.maxSinceScrape[gvk] = size
	}
}

// Describe implements prometheus.Collector.
func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.maxDesc
	ch <- m.currentDesc
}

// Collect implements prometheus.Collector. It emits the current size and the
// high-watermark per GVK, then resets the high-watermark so the next scrape
// captures a fresh maximum.
func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for gvk, size := range m.current {
		ch <- prometheus.MustNewConstMetric(
			m.currentDesc, prometheus.GaugeValue, float64(size), gvk.String(),
		)
	}
	for gvk, max := range m.maxSinceScrape {
		ch <- prometheus.MustNewConstMetric(
			m.maxDesc, prometheus.GaugeValue, float64(max), gvk.String(),
		)
	}
	// Reset the high-watermark after emitting so the next scrape window starts
	// fresh. current is intentionally left as-is (it is a snapshot).
	m.maxSinceScrape = make(map[schema.GroupVersionKind]int)
}

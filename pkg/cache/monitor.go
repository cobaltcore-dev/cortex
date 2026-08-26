// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// gvkHostKey identifies one overlay-size series: a cached GVK on a specific
// cluster (identified by its rest config Host). One Monitor is shared across
// all overlays (home + remotes), so the host discriminates between them.
type gvkHostKey struct {
	gvk  schema.GroupVersionKind
	host string
}

// sizeStat holds the current and high-watermark entry counts for one series.
type sizeStat struct {
	current int
	max     int
}

// Monitor is a standalone prometheus.Collector that tracks the overlay's
// per-(gvk, host) entry count. It exposes both the current size and a
// high-watermark that is reset to the current size on every Collect, so
// transient spikes are captured even when the overlay is empty at scrape time.
//
// Overlay entries are extremely short-lived (evicted within milliseconds once
// the informer observes the write), so a plain gauge sampled at scrape time
// would almost always read zero. A consistently non-zero high-watermark is a
// strong signal that eviction is broken (informer lag, UID mismatch, TTL
// fallback firing).
type Monitor struct {
	maxDesc     *prometheus.Desc
	currentDesc *prometheus.Desc

	mu    sync.Mutex
	stats map[gvkHostKey]*sizeStat
}

// NewMonitor returns a Monitor whose metric names are prefixed with prefix
// (e.g. "cortex_" yields "cortex_cache_overlay_entries").
func NewMonitor(prefix string) *Monitor {
	labels := []string{"gvk", "host"}
	return &Monitor{
		maxDesc: prometheus.NewDesc(
			prefix+"cache_overlay_entries_max",
			"High-watermark of pending-cache overlay entries (live + tombstone) "+
				"per gvk and host since the last scrape; reset on each scrape.",
			labels, nil,
		),
		currentDesc: prometheus.NewDesc(
			prefix+"cache_overlay_entries",
			"Current number of pending-cache overlay entries (live + tombstone) "+
				"per gvk and host at scrape time.",
			labels, nil,
		),
		stats: make(map[gvkHostKey]*sizeStat),
	}
}

// observeSize records that the overlay for (host, gvk) currently holds n
// entries, bumping the high-watermark if n exceeds it. It is called by the
// Overlay under its own lock after every entry-count change. Nil-safe: a nil
// receiver (cache running without a monitor) is a no-op.
func (m *Monitor) observeSize(host string, gvk schema.GroupVersionKind, n int) {
	if m == nil {
		return
	}
	key := gvkHostKey{gvk: gvk, host: host}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.stats[key]
	if s == nil {
		s = &sizeStat{}
		m.stats[key] = s
	}
	s.current = n
	if n > s.max {
		s.max = n
	}
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.maxDesc
	ch <- m.currentDesc
}

// Collect implements prometheus.Collector. It emits the current size and the
// high-watermark for every observed series, then resets each high-watermark to
// the current size so the next scrape measures a fresh window.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.stats {
		gvkStr := key.gvk.GroupVersion().String() + "/" + key.gvk.Kind
		ch <- prometheus.MustNewConstMetric(
			m.currentDesc, prometheus.GaugeValue, float64(s.current), gvkStr, key.host,
		)
		ch <- prometheus.MustNewConstMetric(
			m.maxDesc, prometheus.GaugeValue, float64(s.max), gvkStr, key.host,
		)
		// Reset the window: the next scrape measures the peak since now.
		s.max = s.current
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// gvkString mirrors the label the Monitor emits for a GVK.
func gvkString(gvk schema.GroupVersionKind) string {
	return gvk.GroupVersion().String() + "/" + gvk.Kind
}

// gaugeValue gathers the monitor via a registry and returns the value of the
// named metric for the given gvk/host labels. It reports whether a series with
// those labels was found.
func gaugeValue(t *testing.T, m *Monitor, name, gvk, host string) (float64, bool) {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatalf("register monitor: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, met := range f.GetMetric() {
			if labelMatches(met, gvk, host) {
				return met.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func labelMatches(met *dto.Metric, gvk, host string) bool {
	var gotGVK, gotHost string
	for _, l := range met.GetLabel() {
		switch l.GetName() {
		case "gvk":
			gotGVK = l.GetValue()
		case "host":
			gotHost = l.GetValue()
		}
	}
	return gotGVK == gvk && gotHost == host
}

const (
	maxMetric     = "cortex_cache_overlay_entries_max"
	currentMetric = "cortex_cache_overlay_entries"
)

// TestMonitorWatermarkBeforeScrape checks the max reflects the peak on the very
// first scrape, even after entries are evicted below it.
func TestMonitorWatermarkBeforeScrape(t *testing.T) {
	m := NewMonitor("cortex_")
	gvk := reservationGVK()
	host := "cluster-a"
	gvkStr := gvkString(gvk)

	m.observeSize(host, gvk, 1)
	m.observeSize(host, gvk, 2)
	m.observeSize(host, gvk, 3)
	m.observeSize(host, gvk, 1) // evicted back down

	if got, ok := gaugeValue(t, m, maxMetric, gvkStr, host); !ok || got != 3 {
		t.Fatalf("max: got %v (found=%v), want 3", got, ok)
	}
}

// TestMonitorCollectResetsWindow verifies the high-watermark resets to the
// current size after a Collect, so the next scrape measures a fresh window.
func TestMonitorCollectResetsWindow(t *testing.T) {
	m := NewMonitor("cortex_")
	gvk := reservationGVK()
	host := "cluster-a"
	gvkStr := gvkString(gvk)

	m.observeSize(host, gvk, 3)
	m.observeSize(host, gvk, 1)

	// First scrape sees the peak of 3 and resets max to current (1).
	if got, _ := gaugeValue(t, m, maxMetric, gvkStr, host); got != 3 {
		t.Fatalf("first scrape max: got %v, want 3", got)
	}
	// Second scrape (no further writes) reports max == current == 1.
	maxOverTime, _ := gaugeValue(t, m, maxMetric, gvkStr, host)
	cur, _ := gaugeValue(t, m, currentMetric, gvkStr, host)
	if maxOverTime != cur || maxOverTime != 1 {
		t.Fatalf("second scrape: max=%v current=%v, want both 1", maxOverTime, cur)
	}
}

// TestMonitorCurrentTracksLiveSize verifies the current gauge follows the real
// entry count as writes and evictions happen through the Overlay.
func TestMonitorCurrentTracksLiveSize(t *testing.T) {
	m := NewMonitor("cortex_")
	c := cachingFrom(t, newTestClient(t).fakeCluster, reservationConfig())
	c.monitor = m
	c.host = "cluster-a"
	gvk := reservationGVK()
	gvkStr := gvkString(gvk)

	// Add two live entries.
	c.upsert(gvk, newReservation("res-1", "az-1", "1"))
	c.upsert(gvk, newReservation("res-2", "az-1", "1"))
	if got, ok := gaugeValue(t, m, currentMetric, gvkStr, "cluster-a"); !ok || got != 2 {
		t.Fatalf("after upserts: got %v (found=%v), want 2", got, ok)
	}

	// Evict both via informer-observed objects at a >= ResourceVersion.
	c.evictIfSeen(gvk, newReservation("res-1", "az-1", "1"))
	c.evictIfSeen(gvk, newReservation("res-2", "az-1", "1"))
	if got, _ := gaugeValue(t, m, currentMetric, gvkStr, "cluster-a"); got != 0 {
		t.Fatalf("after evictions: got %v, want 0", got)
	}
}

// TestMonitorCountsTombstones verifies both live entries and tombstones are
// counted in the overlay size.
func TestMonitorCountsTombstones(t *testing.T) {
	m := NewMonitor("cortex_")
	c := cachingFrom(t, newTestClient(t).fakeCluster, reservationConfig())
	c.monitor = m
	c.host = "cluster-a"
	gvk := reservationGVK()
	gvkStr := gvkString(gvk)

	c.upsert(gvk, newReservation("res-live", "az-1", "1"))
	c.tombstone(gvk, newReservation("res-dead", "az-1", "1"))
	if got, ok := gaugeValue(t, m, currentMetric, gvkStr, "cluster-a"); !ok || got != 2 {
		t.Fatalf("live+tombstone: got %v (found=%v), want 2", got, ok)
	}
}

// TestMonitorPerHostSeparation verifies that two overlays sharing one Monitor
// but reporting different hosts produce distinct series.
func TestMonitorPerHostSeparation(t *testing.T) {
	m := NewMonitor("cortex_")
	gvk := reservationGVK()
	gvkStr := gvkString(gvk)

	m.observeSize("cluster-a", gvk, 5)
	m.observeSize("cluster-b", gvk, 2)

	if got, ok := gaugeValue(t, m, currentMetric, gvkStr, "cluster-a"); !ok || got != 5 {
		t.Fatalf("cluster-a: got %v (found=%v), want 5", got, ok)
	}
	if got, ok := gaugeValue(t, m, currentMetric, gvkStr, "cluster-b"); !ok || got != 2 {
		t.Fatalf("cluster-b: got %v (found=%v), want 2", got, ok)
	}
}

// TestMonitorNilSafe verifies that a nil *Monitor and an Overlay with no monitor
// do not panic on size observation.
func TestMonitorNilSafe(t *testing.T) {
	var m *Monitor
	m.observeSize("host", reservationGVK(), 3) // must not panic

	// An Overlay built with a nil monitor (via WrapCluster(..., nil)) must not
	// panic when its entry count changes.
	c := cachingFrom(t, newTestClient(t).fakeCluster, reservationConfig())
	if c.monitor != nil {
		t.Fatalf("expected nil monitor on default overlay")
	}
	c.upsert(reservationGVK(), newReservation("res-nil", "az-1", "1"))
}

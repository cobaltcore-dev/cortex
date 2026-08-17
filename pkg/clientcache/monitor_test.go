// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// scrape performs a single registry Gather (which triggers one Collect, and
// thus one high-watermark reset) and returns the gauge values keyed by
// "<metric name>|<gvk label>".
func scrape(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := make(map[string]float64)
	for _, f := range families {
		for _, m := range f.GetMetric() {
			out[f.GetName()+"|"+labelValue(m, "gvk")] = m.GetGauge().GetValue()
		}
	}
	return out
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func TestMonitor_Registration(t *testing.T) {
	m := NewMonitor("cortex_")
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatalf("register: %v", err)
	}

	gvk := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	m.observe(gvk, 2)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var maxFound, curFound bool
	for _, f := range families {
		switch f.GetName() {
		case "cortex_clientcache_overlay_entries_max":
			maxFound = true
		case "cortex_clientcache_overlay_entries":
			curFound = true
		}
	}
	if !maxFound {
		t.Error("expected cortex_clientcache_overlay_entries_max to be registered")
	}
	if !curFound {
		t.Error("expected cortex_clientcache_overlay_entries to be registered")
	}
}

func TestMonitor_HighWatermarkAndReset(t *testing.T) {
	m := NewMonitor("cortex_")
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatalf("register: %v", err)
	}

	gvk := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	otherGVK := schema.GroupVersionKind{Group: "kvm.cloud.sap", Version: "v1", Kind: "Hypervisor"}

	// Observe a spike (max 3) that drains back to 2.
	m.observe(gvk, 1)
	m.observe(gvk, 3)
	m.observe(gvk, 2)
	// A second GVK stays independent.
	m.observe(otherGVK, 5)

	// First scrape: max reflects the spike, current the last observed size.
	const maxName = "cortex_clientcache_overlay_entries_max"
	const curName = "cortex_clientcache_overlay_entries"
	s := scrape(t, reg)
	if got := s[maxName+"|"+gvk.String()]; got != 3 {
		t.Errorf("first scrape max for %s: got %v, want 3", gvk, got)
	}
	if got := s[curName+"|"+gvk.String()]; got != 2 {
		t.Errorf("first scrape current for %s: got %v, want 2", gvk, got)
	}
	if got := s[maxName+"|"+otherGVK.String()]; got != 5 {
		t.Errorf("first scrape max for %s: got %v, want 5", otherGVK, got)
	}

	// Second scrape without further observation: the high-watermark was reset
	// by the previous Collect, so no _max sample remains for the gvk. current
	// is a snapshot and still reports the last size.
	s = scrape(t, reg)
	if _, ok := s[maxName+"|"+gvk.String()]; ok {
		t.Errorf("second scrape max for %s: expected reset (absent), got a sample", gvk)
	}
	if got := s[curName+"|"+gvk.String()]; got != 2 {
		t.Errorf("second scrape current for %s: got %v, want 2", gvk, got)
	}

	// Observing again after reset with size 0 does not create a _max sample
	// (observe only records a watermark when size grows above the current max,
	// and 0 never exceeds the zero-valued default). The current gauge reflects
	// the new size of 0.
	m.observe(gvk, 0)
	s = scrape(t, reg)
	if _, ok := s[maxName+"|"+gvk.String()]; ok {
		t.Errorf("post-reset max for %s: expected absent for size 0, got a sample", gvk)
	}
	if got, ok := s[curName+"|"+gvk.String()]; !ok || got != 0 {
		t.Errorf("post-reset current for %s: got %v (ok=%v), want 0", gvk, got, ok)
	}

	// Observing a non-zero size after reset re-establishes the watermark.
	m.observe(gvk, 4)
	s = scrape(t, reg)
	if got := s[maxName+"|"+gvk.String()]; got != 4 {
		t.Errorf("post-reset max for %s: got %v, want 4", gvk, got)
	}
}

// TestMonitor_ClientRecords exercises the client-level recording path with a
// real monitor: creating and deleting a cached object moves the overlay size
// and the high-watermark tracks the peak.
func TestMonitor_ClientRecords(t *testing.T) {
	inner := newTestClient(t)
	m := NewMonitor("cortex_")
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatalf("register: %v", err)
	}
	c, err := New(inner, testScheme(t), reservationConfig(), m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := c.Create(ctx, newReservation("a", "az1", "")); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if err := c.Create(ctx, newReservation("b", "az1", "")); err != nil {
		t.Fatalf("Create b: %v", err)
	}
	// Delete tombstones stay in the overlay, so the size does not shrink here.
	if err := c.Delete(ctx, newReservation("a", "az1", "")); err != nil {
		t.Fatalf("Delete a: %v", err)
	}

	gvk := reservationGVK().String()
	s := scrape(t, reg)
	if got := s["cortex_clientcache_overlay_entries_max|"+gvk]; got != 2 {
		t.Errorf("max: got %v, want 2", got)
	}
	if got := s["cortex_clientcache_overlay_entries|"+gvk]; got != 2 {
		t.Errorf("current: got %v, want 2", got)
	}
}

// TestMonitor_SeedOnNew verifies that New seeds the monitor so that
// cortex_clientcache_overlay_entries appears immediately (as 0) without any
// writes. The _max metric must remain absent until at least one write occurs.
func TestMonitor_SeedOnNew(t *testing.T) {
	inner := newTestClient(t)
	m := NewMonitor("cortex_")
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := New(inner, testScheme(t), reservationConfig(), m); err != nil {
		t.Fatalf("New: %v", err)
	}

	gvk := reservationGVK().String()
	s := scrape(t, reg)
	if got, ok := s["cortex_clientcache_overlay_entries|"+gvk]; !ok || got != 0 {
		t.Errorf("seeded current: got %v (ok=%v), want 0", got, ok)
	}
	if _, ok := s["cortex_clientcache_overlay_entries_max|"+gvk]; ok {
		t.Errorf("seeded max: expected absent before any writes, got a sample")
	}
}

// TestMonitor_NilSafe verifies a client built with a nil monitor does not panic
// on the recording paths.
func TestMonitor_NilSafe(t *testing.T) {
	inner := newTestClient(t)
	c, err := New(inner, testScheme(t), reservationConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	res := newReservation("a", "az1", "")
	if err := c.Create(ctx, res); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := c.Delete(ctx, res); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	c.cleanupExpired(res.CreationTimestamp.Time)
}

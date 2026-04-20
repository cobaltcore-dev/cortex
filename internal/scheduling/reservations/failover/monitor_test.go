// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gatherMetricFamilies(t *testing.T, monitor *FailoverMonitor) map[string]*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewRegistry()
	if err := registry.Register(monitor); err != nil {
		t.Fatalf("failed to register monitor: %v", err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	result := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		result[*f.Name] = f
	}
	return result
}

func findMetricWithAZ(family *dto.MetricFamily, az string) *dto.Metric {
	for _, m := range family.Metric {
		for _, l := range m.Label {
			if *l.Name == "availability_zone" && *l.Value == az {
				return m
			}
		}
	}
	return nil
}

func TestFailoverMonitor_MetricsRegistration(t *testing.T) {
	monitor := NewFailoverMonitor()

	// Record a reconciliation so all metrics have values
	monitor.RecordReconciliation(reconcileSummary{
		duration:            500 * time.Millisecond,
		totalVMs:            100,
		totalReservations:   50,
		vmsMissingFailover:  5,
		vmsProcessed:        10,
		reservationsNeeded:  8,
		totalReused:         3,
		totalCreated:        4,
		totalFailed:         1,
		reservationsUpdated: 2,
		reservationsDeleted: 1,
	}, "")

	families := gatherMetricFamilies(t, monitor)

	expectedMetrics := map[string]dto.MetricType{
		"cortex_failover_reconciliation_runs_total":                 dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_duration_seconds":           dto.MetricType_HISTOGRAM,
		"cortex_failover_reconciliation_total_vms":                  dto.MetricType_GAUGE,
		"cortex_failover_reconciliation_total_reservations":         dto.MetricType_GAUGE,
		"cortex_failover_reconciliation_vms_missing_failover":       dto.MetricType_GAUGE,
		"cortex_failover_reconciliation_vms_processed_total":        dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_needed_total":  dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_reused_total":  dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_created_total": dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_failed_total":  dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_updated_total": dto.MetricType_COUNTER,
		"cortex_failover_reconciliation_reservations_deleted_total": dto.MetricType_COUNTER,
	}

	for name, expectedType := range expectedMetrics {
		family, ok := families[name]
		if !ok {
			t.Errorf("metric %q not found in registry", name)
			continue
		}
		if *family.Type != expectedType {
			t.Errorf("metric %q: expected type %v, got %v", name, expectedType, *family.Type)
		}
	}
}

func TestFailoverMonitor_RecordReconciliation(t *testing.T) {
	tests := []struct {
		name             string
		summaries        []reconcileSummary
		azs              []string
		wantRuns         float64
		wantGaugeVMs     float64
		wantGaugeRes     float64
		wantGaugeMissing float64
		wantProcessed    float64
		wantCreated      float64
		wantFailed       float64
		wantReused       float64
		wantNeeded       float64
		wantUpdated      float64
		wantDeleted      float64
		checkAZ          string
	}{
		{
			name: "single reconciliation",
			summaries: []reconcileSummary{{
				duration:            500 * time.Millisecond,
				totalVMs:            100,
				totalReservations:   50,
				vmsMissingFailover:  5,
				vmsProcessed:        10,
				reservationsNeeded:  8,
				totalReused:         3,
				totalCreated:        4,
				totalFailed:         1,
				reservationsUpdated: 2,
				reservationsDeleted: 1,
			}},
			azs:              []string{""},
			wantRuns:         1,
			wantGaugeVMs:     100,
			wantGaugeRes:     50,
			wantGaugeMissing: 5,
			wantProcessed:    10,
			wantCreated:      4,
			wantFailed:       1,
			wantReused:       3,
			wantNeeded:       8,
			wantUpdated:      2,
			wantDeleted:      1,
			checkAZ:          "",
		},
		{
			name: "counters accumulate across runs",
			summaries: []reconcileSummary{
				{duration: 100 * time.Millisecond, totalVMs: 100, totalReservations: 50, vmsProcessed: 10, totalCreated: 4, totalFailed: 1, totalReused: 3, reservationsNeeded: 8, reservationsUpdated: 2, reservationsDeleted: 1, vmsMissingFailover: 5},
				{duration: 200 * time.Millisecond, totalVMs: 95, totalReservations: 52, vmsProcessed: 5, totalCreated: 2, totalFailed: 0, totalReused: 1, reservationsNeeded: 3, reservationsUpdated: 1, reservationsDeleted: 0, vmsMissingFailover: 3},
			},
			azs:              []string{"", ""},
			wantRuns:         2,
			wantGaugeVMs:     95,
			wantGaugeRes:     52,
			wantGaugeMissing: 3,
			wantProcessed:    15,
			wantCreated:      6,
			wantFailed:       1,
			wantReused:       4,
			wantNeeded:       11,
			wantUpdated:      3,
			wantDeleted:      1,
			checkAZ:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := NewFailoverMonitor()

			for i, s := range tt.summaries {
				monitor.RecordReconciliation(s, tt.azs[i])
			}

			families := gatherMetricFamilies(t, monitor)

			assertCounter(t, families, "cortex_failover_reconciliation_runs_total", tt.checkAZ, tt.wantRuns)
			assertGauge(t, families, "cortex_failover_reconciliation_total_vms", tt.checkAZ, tt.wantGaugeVMs)
			assertGauge(t, families, "cortex_failover_reconciliation_total_reservations", tt.checkAZ, tt.wantGaugeRes)
			assertGauge(t, families, "cortex_failover_reconciliation_vms_missing_failover", tt.checkAZ, tt.wantGaugeMissing)
			assertCounter(t, families, "cortex_failover_reconciliation_vms_processed_total", tt.checkAZ, tt.wantProcessed)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_needed_total", tt.checkAZ, tt.wantNeeded)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_reused_total", tt.checkAZ, tt.wantReused)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_created_total", tt.checkAZ, tt.wantCreated)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_failed_total", tt.checkAZ, tt.wantFailed)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_updated_total", tt.checkAZ, tt.wantUpdated)
			assertCounter(t, families, "cortex_failover_reconciliation_reservations_deleted_total", tt.checkAZ, tt.wantDeleted)

			// Verify histogram recorded observations
			histFamily := families["cortex_failover_reconciliation_duration_seconds"]
			if histFamily == nil {
				t.Fatal("duration histogram not found")
			}
			m := findMetricWithAZ(histFamily, tt.checkAZ)
			if m == nil || m.Histogram == nil {
				t.Fatal("duration histogram metric not found for AZ")
			}
			if got := m.Histogram.GetSampleCount(); got != uint64(len(tt.summaries)) {
				t.Errorf("duration histogram sample count: got %d, want %d", got, len(tt.summaries))
			}
		})
	}
}

func TestFailoverMonitor_AvailabilityZoneLabel(t *testing.T) {
	monitor := NewFailoverMonitor()

	monitor.RecordReconciliation(reconcileSummary{
		duration:          100 * time.Millisecond,
		totalVMs:          50,
		totalReservations: 20,
		vmsProcessed:      10,
		totalCreated:      5,
	}, "eu-de-1a")

	monitor.RecordReconciliation(reconcileSummary{
		duration:          200 * time.Millisecond,
		totalVMs:          40,
		totalReservations: 15,
		vmsProcessed:      8,
		totalCreated:      3,
	}, "eu-de-1b")

	families := gatherMetricFamilies(t, monitor)

	assertCounter(t, families, "cortex_failover_reconciliation_runs_total", "eu-de-1a", 1)
	assertCounter(t, families, "cortex_failover_reconciliation_runs_total", "eu-de-1b", 1)
	assertGauge(t, families, "cortex_failover_reconciliation_total_vms", "eu-de-1a", 50)
	assertGauge(t, families, "cortex_failover_reconciliation_total_vms", "eu-de-1b", 40)
	assertCounter(t, families, "cortex_failover_reconciliation_reservations_created_total", "eu-de-1a", 5)
	assertCounter(t, families, "cortex_failover_reconciliation_reservations_created_total", "eu-de-1b", 3)
}

func TestFailoverMonitor_PreInitialization(t *testing.T) {
	monitor := NewFailoverMonitor()

	// Without recording anything, all metrics should still be present with the aggregate label
	families := gatherMetricFamilies(t, monitor)

	expectedMetrics := []string{
		"cortex_failover_reconciliation_runs_total",
		"cortex_failover_reconciliation_duration_seconds",
		"cortex_failover_reconciliation_total_vms",
		"cortex_failover_reconciliation_total_reservations",
		"cortex_failover_reconciliation_vms_missing_failover",
		"cortex_failover_reconciliation_vms_processed_total",
		"cortex_failover_reconciliation_reservations_needed_total",
		"cortex_failover_reconciliation_reservations_reused_total",
		"cortex_failover_reconciliation_reservations_created_total",
		"cortex_failover_reconciliation_reservations_failed_total",
		"cortex_failover_reconciliation_reservations_updated_total",
		"cortex_failover_reconciliation_reservations_deleted_total",
	}

	for _, name := range expectedMetrics {
		family, ok := families[name]
		if !ok {
			t.Errorf("metric %q not found after pre-initialization (no reconciliation recorded)", name)
			continue
		}
		if m := findMetricWithAZ(family, ""); m == nil {
			t.Errorf("metric %q missing aggregate label (availability_zone=\"\")", name)
		}
	}
}

func assertCounter(t *testing.T, families map[string]*dto.MetricFamily, name, az string, expected float64) {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Errorf("counter %q not found", name)
		return
	}
	m := findMetricWithAZ(family, az)
	if m == nil || m.Counter == nil {
		t.Errorf("counter %q with az=%q not found", name, az)
		return
	}
	if got := m.Counter.GetValue(); got != expected {
		t.Errorf("counter %q: got %v, want %v", name, got, expected)
	}
}

func assertGauge(t *testing.T, families map[string]*dto.MetricFamily, name, az string, expected float64) {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Errorf("gauge %q not found", name)
		return
	}
	m := findMetricWithAZ(family, az)
	if m == nil || m.Gauge == nil {
		t.Errorf("gauge %q with az=%q not found", name, az)
		return
	}
	if got := m.Gauge.GetValue(); got != expected {
		t.Errorf("gauge %q: got %v, want %v", name, got, expected)
	}
}

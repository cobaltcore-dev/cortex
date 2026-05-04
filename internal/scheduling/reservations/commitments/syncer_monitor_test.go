// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestSyncerMonitor_MetricsRegistration verifies that all syncer metrics are
// present at zero immediately after registration, without any increments.
// This matters because alert rules reference these metrics and will warn
// "metric missing" if they don't appear before the first sync run.
func TestSyncerMonitor_MetricsRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	monitor := NewSyncerMonitor()

	if err := registry.Register(monitor); err != nil {
		t.Fatalf("failed to register monitor: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	gathered := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		gathered[f.GetName()] = f
	}

	cases := []struct {
		name       string
		metricType dto.MetricType
	}{
		{"cortex_committed_resource_syncer_errors_total", dto.MetricType_COUNTER},
		{"cortex_committed_resource_syncer_commitments_skipped_total", dto.MetricType_COUNTER},
		{"cortex_committed_resource_syncer_cr_creates_total", dto.MetricType_COUNTER},
		{"cortex_committed_resource_syncer_cr_updates_total", dto.MetricType_COUNTER},
		{"cortex_committed_resource_syncer_cr_deletes_total", dto.MetricType_COUNTER},
		{"cortex_committed_resource_syncer_crd_unmatched", dto.MetricType_GAUGE},
	}

	for _, tc := range cases {
		f, ok := gathered[tc.name]
		if !ok {
			t.Errorf("metric %q missing after registration (no increments needed)", tc.name)
			continue
		}
		if f.GetType() != tc.metricType {
			t.Errorf("metric %q: expected type %v, got %v", tc.name, tc.metricType, f.GetType())
		}
	}
}

// TestSyncerMonitor_SkipReasonsPreInitialized verifies that all skip reason
// label combinations are present at zero before any commitment is skipped.
// This prevents "metric missing" warnings for the skipped_total CounterVec.
func TestSyncerMonitor_SkipReasonsPreInitialized(t *testing.T) {
	registry := prometheus.NewRegistry()
	monitor := NewSyncerMonitor()

	if err := registry.Register(monitor); err != nil {
		t.Fatalf("failed to register monitor: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var skippedFamily *dto.MetricFamily
	for _, f := range families {
		if f.GetName() == "cortex_committed_resource_syncer_commitments_skipped_total" {
			skippedFamily = f
			break
		}
	}
	if skippedFamily == nil {
		t.Fatal("cortex_committed_resource_syncer_commitments_skipped_total missing after registration")
	}

	presentReasons := make(map[string]bool)
	for _, m := range skippedFamily.Metric {
		for _, l := range m.Label {
			if l.GetName() == "reason" {
				presentReasons[l.GetValue()] = true
			}
		}
	}

	for _, reason := range []string{
		SkipReasonUnitMismatch,
		SkipReasonUnknownFlavorGroup,
		SkipReasonInvalidResource,
		SkipReasonEmptyUUID,
		SkipReasonNonCompute,
	} {
		if !presentReasons[reason] {
			t.Errorf("skip reason %q not pre-initialized in commitments_skipped_total", reason)
		}
	}
}

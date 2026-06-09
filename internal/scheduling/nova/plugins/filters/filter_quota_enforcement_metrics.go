// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// QuotaEnforcementMetrics holds Prometheus metrics for the quota-enforcement
// nova filter. The filter runs in either "enforce" mode (rejects when no
// headroom) or "shadow" mode (logs/counts what it would have rejected without
// removing hosts). The decisions counter exposes the same outcomes in both
// modes so operators can compare a shadow rollout against the existing
// generic step metrics (e.g. cortex_filter_weigher_pipeline_step_removed_hosts).
type QuotaEnforcementMetrics struct {
	// Decisions is the counter of every accept/reject/skip outcome the filter
	// produces. Labels:
	//   - mode:              "enforce" | "shadow"
	//   - decision:          "accept_cr" | "accept_payg" | "accept_no_quota" |
	//                        "accept_skipped" | "reject"
	//   - resource:          "ram" | "cores" | "instances" | "" (empty when
	//                        the decision is not driven by a single resource)
	//   - availability_zone: AZ string or "" if unknown at decision time
	//   - flavor_group:      hw_version string or "" if unknown at decision time
	Decisions *prometheus.CounterVec
}

// NewQuotaEnforcementMetrics constructs the metrics struct. The returned
// *QuotaEnforcementMetrics implements prometheus.Collector, so the caller is
// expected to register it with a registry (typically metrics.Registry from
// cmd/manager). This matches the Monitor pattern used elsewhere in cortex
// (e.g. db.Monitor, LogMetricsMonitor, PipelineMonitor).
func NewQuotaEnforcementMetrics() *QuotaEnforcementMetrics {
	return &QuotaEnforcementMetrics{
		Decisions: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_nova_filter_quota_enforcement_decisions_total",
				Help: "Decisions made by the FilterQuotaEnforcement nova filter, " +
					"labeled by enforcement mode, decision outcome, the resource " +
					"that drove a reject (if any), availability zone, and flavor group.",
			},
			[]string{"mode", "decision", "resource", "availability_zone", "flavor_group"},
		),
	}
}

// Describe implements prometheus.Collector by delegating to the underlying
// counter vec. Nil-safe to mirror RecordDecision's defensive guard: a stray
// MustRegister on an uninitialized struct must not crash the manager.
func (m *QuotaEnforcementMetrics) Describe(ch chan<- *prometheus.Desc) {
	if m == nil || m.Decisions == nil {
		return
	}
	m.Decisions.Describe(ch)
}

// Collect implements prometheus.Collector by delegating to the underlying
// counter vec. Nil-safe — Collect runs on every Prometheus scrape, so a nil
// receiver must not panic the metrics endpoint.
func (m *QuotaEnforcementMetrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil || m.Decisions == nil {
		return
	}
	m.Decisions.Collect(ch)
}

// recordDecisionNilOnce ensures the "metrics not initialized" warning is
// emitted at most once per process to keep logs clean while still surfacing
// misconfigurations. A pointer is used so tests can swap in a freshly armed
// once without copying a sync.Once value (which is forbidden after first use).
var recordDecisionNilOnce = &sync.Once{}

// RecordDecision is a small helper that nil-guards the singleton and increments
// the decisions counter. Filter Run() uses it directly. If the receiver is nil
// (i.e. QuotaEnforcementMetricsSingleton was never initialized — typically a
// missing wiring step in cmd/manager) we log a warn-level message exactly once
// so the misconfiguration is observable both in logs and via
// cortex_log_messages_total{level="warn"}.
func (m *QuotaEnforcementMetrics) RecordDecision(mode, decision, resource, az, flavorGroup string) {
	if m == nil || m.Decisions == nil {
		recordDecisionNilOnce.Do(func() {
			slog.Warn("QuotaEnforcementMetrics is nil; decision metric not recorded "+
				"(is QuotaEnforcementMetricsSingleton initialized in cmd/manager?)",
				"mode", mode,
				"decision", decision,
				"resource", resource,
				"availability_zone", az,
				"flavor_group", flavorGroup,
			)
		})
		return
	}
	m.Decisions.WithLabelValues(mode, decision, resource, az, flavorGroup).Inc()
}

// QuotaEnforcementMetricsSingleton is set from cmd/manager/main.go during
// initialization. The filter's Run method reads it via RecordDecision, which
// nil-guards itself, so unit tests that construct a bare FilterQuotaEnforcement
// without setting this singleton stay safe.
var QuotaEnforcementMetricsSingleton *QuotaEnforcementMetrics

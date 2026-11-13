// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package changes

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

var changeMonitorLog = ctrl.Log.WithName("committed-resource-change-monitor").WithValues("module", "committed-resources")

// ChangeReconcilerMonitor exposes the cortex_committed_resource_change_stuck metric: non-terminal
// change requests that have stayed in their current phase longer than StuckThreshold, grouped by
// phase and AZ. These are the ones to alert on — they are never garbage-collected. The metric is
// absent for a label set when no change requests match; absence means zero.
type ChangeReconcilerMonitor struct {
	client         client.Client
	stuckThreshold time.Duration
	stuck          *prometheus.Desc
}

func NewChangeReconcilerMonitor(c client.Client, stuckThreshold time.Duration) ChangeReconcilerMonitor {
	return ChangeReconcilerMonitor{
		client:         c,
		stuckThreshold: stuckThreshold,
		stuck: prometheus.NewDesc(
			"cortex_committed_resource_change_stuck",
			"Number of non-terminal CommittedResourceChanges stuck in their current phase beyond the configured threshold.",
			[]string{"phase", "availability_zone"},
			nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (m *ChangeReconcilerMonitor) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.stuck
}

// Collect implements prometheus.Collector. Lists all CommittedResourceChanges and counts
// non-terminal ones whose time in the current phase exceeds the stuck threshold, grouped by
// phase and AZ.
func (m *ChangeReconcilerMonitor) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var list v1alpha1.CommittedResourceChangeList
	if err := m.client.List(ctx, &list); err != nil {
		changeMonitorLog.Error(err, "failed to list CommittedResourceChanges")
		return
	}

	type key struct{ phase, az string }
	counts := make(map[key]int)
	for i := range list.Items {
		tx := &list.Items[i]
		if tx.IsTerminal() || !tx.DeletionTimestamp.IsZero() {
			continue
		}
		if time.Since(phaseSince(tx)) <= m.stuckThreshold {
			continue
		}
		counts[key{phase: string(tx.Status.Phase), az: tx.Spec.AvailabilityZone}]++
	}

	for k, count := range counts {
		ch <- prometheus.MustNewConstMetric(m.stuck, prometheus.GaugeValue, float64(count), k.phase, k.az)
	}
}

// phaseSince returns how long the change request has been in its current phase, derived from the
// Progressing condition's LastTransitionTime (updated on every phase advance), falling back to the
// creation timestamp for requests that have not yet been reconciled.
func phaseSince(tx *v1alpha1.CommittedResourceChange) time.Time {
	if cond := meta.FindStatusCondition(tx.Status.Conditions, v1alpha1.ChangeConditionProgressing); cond != nil {
		return cond.LastTransitionTime.Time
	}
	return tx.CreationTimestamp.Time
}

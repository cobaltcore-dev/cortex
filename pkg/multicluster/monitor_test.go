// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMonitor_Registration(t *testing.T) {
	monitor := NewMonitor()

	registry := prometheus.NewRegistry()
	if err := registry.Register(monitor); err != nil {
		t.Fatalf("failed to register monitor: %v", err)
	}

	// The counter has no values until something is recorded, so it does not
	// appear in the gathered families yet. Recording one makes it show up.
	gvk := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	monitor.recordCrossClusterNameConflict("create", gvk)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	var found bool
	for _, f := range families {
		if f.GetName() == "cortex_multicluster_cross_cluster_name_conflicts_total" {
			found = true
		}
	}
	if !found {
		t.Error("expected cortex_multicluster_cross_cluster_name_conflicts_total to be registered")
	}
}

func TestMonitor_RecordCrossClusterNameConflict(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	otherGVK := schema.GroupVersionKind{Group: "kvm.cloud.sap", Version: "v1", Kind: "Hypervisor"}

	monitor := NewMonitor()

	// Recording accumulates per (method, gvk) label pair.
	monitor.recordCrossClusterNameConflict("create", gvk)
	monitor.recordCrossClusterNameConflict("create", gvk)
	monitor.recordCrossClusterNameConflict("get", gvk)
	monitor.recordCrossClusterNameConflict("list", otherGVK)

	if got := testutil.ToFloat64(monitor.crossClusterNameConflicts.WithLabelValues("create", gvk.String())); got != 2 {
		t.Errorf("create/%s: got %v, want 2", gvk, got)
	}
	if got := testutil.ToFloat64(monitor.crossClusterNameConflicts.WithLabelValues("get", gvk.String())); got != 1 {
		t.Errorf("get/%s: got %v, want 1", gvk, got)
	}
	if got := testutil.ToFloat64(monitor.crossClusterNameConflicts.WithLabelValues("list", otherGVK.String())); got != 1 {
		t.Errorf("list/%s: got %v, want 1", otherGVK, got)
	}
	// A label pair that was never recorded stays at zero.
	if got := testutil.ToFloat64(monitor.crossClusterNameConflicts.WithLabelValues("list", gvk.String())); got != 0 {
		t.Errorf("list/%s: got %v, want 0", gvk, got)
	}
}

func TestMonitor_RecordCrossClusterNameConflict_NilSafe(t *testing.T) {
	var monitor *Monitor
	gvk := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}

	// Recording on a nil monitor must be a no-op and must not panic.
	monitor.recordCrossClusterNameConflict("create", gvk)
}

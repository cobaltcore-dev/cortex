// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
)

func TestGroupOverlap(t *testing.T) {
	tests := []struct {
		name            string
		groups          []GroupInput
		wantGroups      []string
		wantSharedHosts int
	}{
		{
			name:            "no groups",
			groups:          nil,
			wantGroups:      []string{},
			wantSharedHosts: 0,
		},
		{
			name: "single group has no overlap",
			groups: []GroupInput{
				{Name: "hana", CandidateHosts: []string{"h1", "h2"}},
			},
			wantGroups:      []string{"hana"},
			wantSharedHosts: 0,
		},
		{
			name: "disjoint groups have no overlap",
			groups: []GroupInput{
				{Name: "hana", CandidateHosts: []string{"h1", "h2"}},
				{Name: "general", CandidateHosts: []string{"h3", "h4"}},
			},
			wantGroups:      []string{"general", "hana"},
			wantSharedHosts: 0,
		},
		{
			name: "one shared host between two groups",
			groups: []GroupInput{
				{Name: "hana", CandidateHosts: []string{"h1", "h2"}},
				{Name: "general", CandidateHosts: []string{"h2", "h3"}},
			},
			wantGroups:      []string{"general", "hana"},
			wantSharedHosts: 1,
		},
		{
			name: "host shared by three groups counts once",
			groups: []GroupInput{
				{Name: "a", CandidateHosts: []string{"h1"}},
				{Name: "b", CandidateHosts: []string{"h1"}},
				{Name: "c", CandidateHosts: []string{"h1"}},
			},
			wantGroups:      []string{"a", "b", "c"},
			wantSharedHosts: 1,
		},
		{
			name: "duplicate candidate within a group does not create overlap",
			groups: []GroupInput{
				{Name: "hana", CandidateHosts: []string{"h1", "h1"}},
			},
			wantGroups:      []string{"hana"},
			wantSharedHosts: 0,
		},
		{
			name: "groups returned sorted regardless of input order",
			groups: []GroupInput{
				{Name: "zeta", CandidateHosts: []string{"h1"}},
				{Name: "alpha", CandidateHosts: []string{"h1"}},
			},
			wantGroups:      []string{"alpha", "zeta"},
			wantSharedHosts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGroups, gotShared := groupOverlap(tt.groups)
			if gotShared != tt.wantSharedHosts {
				t.Errorf("sharedHostCount = %d, want %d", gotShared, tt.wantSharedHosts)
			}
			if len(gotGroups) != len(tt.wantGroups) {
				t.Fatalf("participating groups = %v, want %v", gotGroups, tt.wantGroups)
			}
			for i := range gotGroups {
				if gotGroups[i] != tt.wantGroups[i] {
					t.Fatalf("participating groups = %v, want %v", gotGroups, tt.wantGroups)
				}
			}
		})
	}
}

func TestSplitMetricsRecordStranded(t *testing.T) {
	m := NewSplitMetrics(prometheus.NewRegistry())
	m.RecordStranded("az1", "general,hana", map[string]int64{
		ResourceMemory: 16 * GiB,
		ResourceCores:  8,
	})

	if got := testutil.ToFloat64(m.stranded.WithLabelValues("az1", ResourceMemory, "general,hana")); got != float64(16*GiB) {
		t.Errorf("stranded memory = %v, want %v", got, float64(16*GiB))
	}
	if got := testutil.ToFloat64(m.stranded.WithLabelValues("az1", ResourceCores, "general,hana")); got != 8 {
		t.Errorf("stranded cores = %v, want 8", got)
	}
}

func TestSplitMetricsRecordStrandedZeroEmitsSeries(t *testing.T) {
	m := NewSplitMetrics(prometheus.NewRegistry())
	// Empty map: both resources should still be emitted as zero so a healthy
	// state is observable rather than an absent series.
	m.RecordStranded("az1", "hana", map[string]int64{})

	if got := testutil.CollectAndCount(m.stranded); got != 2 {
		t.Errorf("stranded series count = %d, want 2 (memory + cores)", got)
	}
	if got := testutil.ToFloat64(m.stranded.WithLabelValues("az1", ResourceMemory, "hana")); got != 0 {
		t.Errorf("stranded memory = %v, want 0", got)
	}
}

func TestSplitMetricsRecordOverlap(t *testing.T) {
	m := NewSplitMetrics(prometheus.NewRegistry())
	m.RecordOverlap("az1", "general,hana", 3)

	if got := testutil.ToFloat64(m.overlappingHost.WithLabelValues("az1", "general,hana")); got != 3 {
		t.Errorf("overlapping hosts = %v, want 3", got)
	}
}

func TestSplitMetricsReset(t *testing.T) {
	m := NewSplitMetrics(prometheus.NewRegistry())
	m.RecordStranded("az1", "hana", map[string]int64{ResourceMemory: GiB, ResourceCores: 1})
	m.RecordOverlap("az1", "hana", 2)

	m.Reset()

	if got := testutil.CollectAndCount(m.stranded); got != 0 {
		t.Errorf("stranded series after reset = %d, want 0", got)
	}
	if got := testutil.CollectAndCount(m.overlappingHost); got != 0 {
		t.Errorf("overlapping series after reset = %d, want 0", got)
	}
}

func TestSplitMetricsNilSafe(t *testing.T) {
	var m *SplitMetrics
	// None of these must panic on a nil receiver.
	m.Reset()
	m.RecordStranded("az1", "hana", map[string]int64{ResourceMemory: GiB})
	m.RecordOverlap("az1", "hana", 1)
}

func TestNewSplitMetricsNilRegistry(t *testing.T) {
	// A nil registerer must not panic and must still return usable metrics.
	m := NewSplitMetrics(nil)
	m.RecordOverlap("az1", "hana", 1)
	if got := testutil.ToFloat64(m.overlappingHost.WithLabelValues("az1", "hana")); got != 1 {
		t.Errorf("overlapping hosts = %v, want 1", got)
	}
}

// TestReconcileAZ_RecordsSplitMetrics verifies the reconciler wires the split
// outcomes into the metrics: two flavor groups sharing the same candidate host
// must produce one overlapping host and the AZ's stranded memory must be exported.
func TestReconcileAZ_RecordsSplitMetrics(t *testing.T) {
	const az = "az-a"
	const memMB = 4096
	const flavorBytes = int64(memMB) * 1024 * 1024

	scheme := newTestScheme(t)
	// One host with 10 GiB: fits two 4 GiB slots, leaving 2 GiB stranded once the
	// round-robin split hands one slot to each of the two groups.
	hvObj := newHypervisor("host-1", az, 10*GiB)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	srv := newMockSchedulerServer(t, []string{"host-1"})
	defer srv.Close()

	m := NewSplitMetrics(prometheus.NewRegistry())
	ctrl := newController(t, fakeClient, Config{
		SchedulerURL:      srv.URL,
		TotalPipeline:     "total",
		PlaceablePipeline: "placeable",
	}).WithSplitMetrics(m)

	mkGroup := func(name string) compute.FlavorGroupFeature {
		f := compute.FlavorInGroup{Name: name + "-small", MemoryMB: memMB, VCPUs: 2}
		return compute.FlavorGroupFeature{Name: name, SmallestFlavor: f, Flavors: []compute.FlavorInGroup{f}}
	}
	groups := map[string]compute.FlavorGroupFeature{
		"general": mkGroup("general"),
		"hana":    mkGroup("hana"),
	}
	hvByName := map[string]hv1.Hypervisor{"host-1": *hvObj}

	ctrl.reconcileAZ(context.Background(), az, groups, hvByName, map[string]map[string]int64{}, map[vmUsageKey]vmUsage{})

	const groupsLabel = "general,hana"
	if got := testutil.ToFloat64(m.overlappingHost.WithLabelValues(az, groupsLabel)); got != 1 {
		t.Errorf("overlapping hosts = %v, want 1 (host-1 shared by both groups)", got)
	}
	if got := testutil.ToFloat64(m.stranded.WithLabelValues(az, ResourceMemory, groupsLabel)); got != float64(10*GiB-2*flavorBytes) {
		t.Errorf("stranded memory = %v, want %v (10 GiB − 2 × 4 GiB)", got, float64(10*GiB-2*flavorBytes))
	}
}

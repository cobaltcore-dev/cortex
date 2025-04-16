// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMLifeSpanKPI_histogram(t *testing.T) {
	kpi := &VMLifeSpanKPI{}
	vmLifeSpans := []shared.VMLifeSpan{
		{FlavorName: "flavor1", FlavorID: "id1", Duration: 10},
		{FlavorName: "flavor1", FlavorID: "id1", Duration: 20},
		{FlavorName: "flavor2", FlavorID: "id2", Duration: 30},
	}

	hists, counts, sums := kpi.histogram(vmLifeSpans)

	expectedBuckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)

	// Check histogram keys
	expectedKeys := []string{"flavor1,id1", "flavor2,id2", "all,all"}
	for _, key := range expectedKeys {
		if _, ok := hists[key]; !ok {
			t.Errorf("expected key %s in histogram, but not found", key)
		}
	}

	// Check counts
	if counts["flavor1,id1"] != 2 {
		t.Errorf("expected count for flavor1,id1 to be 2, got %d", counts["flavor1,id1"])
	}
	if counts["flavor2,id2"] != 1 {
		t.Errorf("expected count for flavor2,id2 to be 1, got %d", counts["flavor2,id2"])
	}
	if counts["all,all"] != 3 {
		t.Errorf("expected count for all,all to be 3, got %d", counts["all,all"])
	}

	// Check sums
	if sums["flavor1,id1"] != 30 {
		t.Errorf("expected sum for flavor1,id1 to be 30, got %f", sums["flavor1,id1"])
	}
	if sums["flavor2,id2"] != 30 {
		t.Errorf("expected sum for flavor2,id2 to be 30, got %f", sums["flavor2,id2"])
	}
	if sums["all,all"] != 60 {
		t.Errorf("expected sum for all,all to be 60, got %f", sums["all,all"])
	}

	// Check buckets
	for key, hist := range hists {
		for _, bucket := range expectedBuckets {
			if _, ok := hist[bucket]; !ok {
				t.Errorf("expected bucket %f in histogram for key %s, but not found", bucket, key)
			}
		}
	}
}

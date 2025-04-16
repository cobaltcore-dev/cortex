// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMMigrationStatisticsKPI_histogram(t *testing.T) {
	kpi := &VMMigrationStatisticsKPI{}

	hostResidencies := []shared.VMHostResidency{
		{Type: "type1", FlavorName: "flavor1", FlavorID: "id1", Duration: 100},
		{Type: "type1", FlavorName: "flavor1", FlavorID: "id1", Duration: 200},
		{Type: "type2", FlavorName: "flavor2", FlavorID: "id2", Duration: 300},
	}

	hists, counts, sums := kpi.histogram(hostResidencies)

	expectedBuckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)

	// Check histogram keys
	expectedKeys := []string{
		"type1,flavor1,id1",
		"all,all,all",
		"type2,flavor2,id2",
	}
	for _, key := range expectedKeys {
		if _, ok := hists[key]; !ok {
			t.Errorf("expected key %s in histogram, but not found", key)
		}
	}

	// Check counts
	expectedCounts := map[string]uint64{
		"type1,flavor1,id1": 2,
		"type2,flavor2,id2": 1,
		"all,all,all":       3,
	}
	for key, expectedCount := range expectedCounts {
		if counts[key] != expectedCount {
			t.Errorf("expected count for key %s to be %d, got %d", key, expectedCount, counts[key])
		}
	}

	// Check sums
	expectedSums := map[string]float64{
		"type1,flavor1,id1": 300,
		"type2,flavor2,id2": 300,
		"all,all,all":       600,
	}
	for key, expectedSum := range expectedSums {
		if sums[key] != expectedSum {
			t.Errorf("expected sum for key %s to be %f, got %f", key, expectedSum, sums[key])
		}
	}

	// Check histogram buckets
	for key, hist := range hists {
		for _, bucket := range expectedBuckets {
			if _, ok := hist[bucket]; !ok {
				t.Errorf("expected bucket %f for key %s, but not found", bucket, key)
			}
		}
	}

	// Check that all buckets are filled with 0 if no data falls into them
	for key, hist := range hists {
		for _, bucket := range expectedBuckets {
			if _, ok := hist[bucket]; !ok {
				t.Errorf("bucket %f for key %s is missing", bucket, key)
			}
		}
	}
}

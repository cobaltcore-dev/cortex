// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"reflect"
	"testing"
)

type mockFeature struct {
	keys  []string
	value float64
}

func TestHistogram(t *testing.T) {
	// Mock data
	features := []mockFeature{
		{keys: []string{"key1"}, value: 1.0},
		{keys: []string{"key1", "key2"}, value: 2.5},
		{keys: []string{"key2"}, value: 3.0},
	}
	buckets := []float64{1.0, 2.0, 3.0}

	// Mock key and value functions
	keysFunc := func(f mockFeature) []string {
		return f.keys
	}
	valueFunc := func(f mockFeature) float64 {
		return f.value
	}

	// Call the Histogram function
	hists, counts, sums := Histogram(features, buckets, keysFunc, valueFunc)

	// Expected results
	expectedHists := map[string]map[float64]uint64{
		"key1": {1.0: 1, 2.0: 1, 3.0: 2},
		"key2": {1.0: 0, 2.0: 0, 3.0: 2},
	}
	expectedCounts := map[string]uint64{
		"key1": 2,
		"key2": 2,
	}
	expectedSums := map[string]float64{
		"key1": 3.5,
		"key2": 5.5,
	}

	// Validate results
	if !reflect.DeepEqual(hists, expectedHists) {
		t.Errorf("hists = %v, want %v", hists, expectedHists)
	}
	if !reflect.DeepEqual(counts, expectedCounts) {
		t.Errorf("counts = %v, want %v", counts, expectedCounts)
	}
	if !reflect.DeepEqual(sums, expectedSums) {
		t.Errorf("sums = %v, want %v", sums, expectedSums)
	}
}

func TestHistogram_EmptyFeatures(t *testing.T) {
	// Test with no features
	features := []mockFeature{}
	buckets := []float64{1.0, 2.0, 3.0}

	keysFunc := func(f mockFeature) []string {
		return f.keys
	}
	valueFunc := func(f mockFeature) float64 {
		return f.value
	}

	hists, counts, sums := Histogram(features, buckets, keysFunc, valueFunc)

	if len(hists) != 0 {
		t.Errorf("expected no histograms, got %v", hists)
	}
	if len(counts) != 0 {
		t.Errorf("expected no counts, got %v", counts)
	}
	if len(sums) != 0 {
		t.Errorf("expected no sums, got %v", sums)
	}
}

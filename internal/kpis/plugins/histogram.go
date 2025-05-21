// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import "github.com/cobaltcore-dev/cortex/extractor/plugins"

// Create a histogram from features.
func Histogram[O plugins.Feature](
	features []O,
	buckets []float64,
	keysFunc func(O) []string,
	valueFunc func(O) float64,
) (
	hists map[string]map[float64]uint64, // By key
	counts map[string]uint64, // By key
	sums map[string]float64, // By key
) {

	hists = map[string]map[float64]uint64{}
	counts = map[string]uint64{}
	sums = map[string]float64{}
	for _, feature := range features {
		keys := keysFunc(feature)
		val := valueFunc(feature)
		for _, key := range keys {
			if _, ok := hists[key]; !ok {
				hists[key] = make(map[float64]uint64, len(buckets))
			}
			for _, bucket := range buckets {
				if val <= bucket {
					hists[key][bucket]++
				}
			}
			counts[key]++
			sums[key] += val
		}
	}
	// Fill up empty buckets
	for key, hist := range hists {
		for _, bucket := range buckets {
			if _, ok := hist[bucket]; !ok {
				hists[key][bucket] = 0
			}
		}
	}
	return hists, counts, sums
}

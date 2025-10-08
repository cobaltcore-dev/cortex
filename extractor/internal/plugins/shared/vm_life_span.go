// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type VMLifeSpanRaw struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
}

// Extractor that extracts the time elapsed until the vm was deleted.
type VMLifeSpanHistogramExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                         // No options passed through yaml config
		shared.VMLifeSpanHistogramBucket, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VMLifeSpanHistogramExtractor) GetName() string {
	return "vm_life_span_histogram_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VMLifeSpanHistogramExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaFlavorsSynced,
	}
}

//go:embed vm_life_span.sql
var vmLifeSpanQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMLifeSpanHistogramExtractor) Extract() ([]plugins.Feature, error) {
	var lifeSpansRaw []VMLifeSpanRaw
	if _, err := e.DB.Select(&lifeSpansRaw, vmLifeSpanQuery); err != nil {
		return nil, err
	}
	// Calculate the histogram based on the extracted features.
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(lifeSpan VMLifeSpanRaw) []string {
		return []string{lifeSpan.FlavorName, "all"}
	}
	valueFunc := func(lifeSpan VMLifeSpanRaw) float64 {
		return float64(lifeSpan.Duration)
	}
	hists, counts, sums := tools.Histogram(lifeSpansRaw, buckets, keysFunc, valueFunc)
	var features []shared.VMLifeSpanHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			slog.Warn("vm_life_span: unexpected comma in flavor name")
			continue
		}
		for bucket, value := range hist {
			// Create a feature for each bucket.
			features = append(features, shared.VMLifeSpanHistogramBucket{
				FlavorName: labels[0],
				Bucket:     bucket,
				Value:      value,
				Count:      counts[key],
				Sum:        sums[key],
			})
		}
	}
	return e.Extracted(features)
}

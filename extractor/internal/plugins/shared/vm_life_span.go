// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/lib/tools"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	"github.com/prometheus/client_golang/prometheus"
)

type VMLifeSpanRaw struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
	// Whether the statistic is from a VM that is deleted or still running.
	Deleted bool `db:"deleted"`
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
		nova.TriggerNovaDeletedServersSynced,
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaFlavorsSynced,
	}
}

//go:embed vm_life_span.sql
var vmLifeSpanQuery string

// Extract histogram buckets from raw life span data based on whether the VMs are deleted or still running.
func extractHistogramBuckets(lifeSpansRaw []VMLifeSpanRaw, deleted bool) []shared.VMLifeSpanHistogramBucket {
	// Filter life spans based on the deleted flag.
	var lifeSpans []VMLifeSpanRaw
	for _, ls := range lifeSpansRaw {
		if ls.Deleted == deleted {
			lifeSpans = append(lifeSpans, ls)
		}
	}

	// Calculate histogram buckets
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(lifeSpan VMLifeSpanRaw) []string {
		return []string{lifeSpan.FlavorName, "all"}
	}
	valueFunc := func(lifeSpan VMLifeSpanRaw) float64 {
		return float64(lifeSpan.Duration)
	}
	hists, counts, sums := tools.Histogram(lifeSpans, buckets, keysFunc, valueFunc)
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
				Deleted:    deleted,
			})
		}
	}
	return features
}

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers (+ deleted servers) to be synced.
func (e *VMLifeSpanHistogramExtractor) Extract() ([]plugins.Feature, error) {
	var lifeSpansRaw []VMLifeSpanRaw
	if _, err := e.DB.Select(&lifeSpansRaw, vmLifeSpanQuery); err != nil {
		return nil, err
	}

	deletedVMLifeSpansBuckets := extractHistogramBuckets(lifeSpansRaw, true)
	runningVMLifeSpansBuckets := extractHistogramBuckets(lifeSpansRaw, false)

	features := append(deletedVMLifeSpansBuckets, runningVMLifeSpansBuckets...)
	return e.Extracted(features)
}

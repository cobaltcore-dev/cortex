// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"
	"errors"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
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

// Feature that describes how long a vm existed before it was deleted.
type VMLifeSpanHistogramBucket struct {
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
	// The bucket this life span falls into.
	Bucket float64 `db:"bucket"`
	// The value of the bucket.
	Value uint64 `db:"value"`
	// The count of vms that fell into this bucket.
	Count uint64 `db:"count"`
	// The sum of all durations that fell into this bucket.
	Sum float64 `db:"sum"`
	// Whether the statistic is of VMs that are deleted or still running.
	Deleted bool `db:"deleted"`
}

// Extractor that extracts the time elapsed until the vm was deleted.
type VMLifeSpanHistogramExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                  // No options passed through yaml config
		VMLifeSpanHistogramBucket, // Feature model
	]
}

// Extract histogram buckets from raw life span data based on whether the VMs are deleted or still running.
func extractHistogramBuckets(lifeSpansRaw []VMLifeSpanRaw, deleted bool) []VMLifeSpanHistogramBucket {
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
	var features []VMLifeSpanHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			slog.Warn("vm_life_span: unexpected comma in flavor name")
			continue
		}
		for bucket, value := range hist {
			// Create a feature for each bucket.
			features = append(features, VMLifeSpanHistogramBucket{
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

//go:embed vm_life_span.sql
var vmLifeSpanQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMLifeSpanHistogramExtractor) Extract() ([]plugins.Feature, error) {
	// This can happen when no datasource is provided that connects to a database.
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	var lifeSpansRaw []VMLifeSpanRaw
	if _, err := e.DB.Select(&lifeSpansRaw, vmLifeSpanQuery); err != nil {
		return nil, err
	}

	deletedVMLifeSpansBuckets := extractHistogramBuckets(lifeSpansRaw, true)
	runningVMLifeSpansBuckets := extractHistogramBuckets(lifeSpansRaw, false)

	deletedVMLifeSpansBuckets = append(deletedVMLifeSpansBuckets, runningVMLifeSpansBuckets...)
	return e.Extracted(deletedVMLifeSpansBuckets)
}

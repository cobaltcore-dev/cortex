// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"
	"errors"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/lib/tools"
	"github.com/prometheus/client_golang/prometheus"
)

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                              // No options passed through yaml config
		shared.VMHostResidencyHistogramBucket, // Feature model
	]
}

type VMHostResidencyRaw struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
}

//go:embed vm_host_residency.sql
var vmHostResidencyQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMHostResidencyExtractor) Extract() ([]plugins.Feature, error) {
	// This can happen when no datasource is provided that connects to a database.
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	var hostResidencies []VMHostResidencyRaw
	if _, err := e.DB.Select(&hostResidencies, vmHostResidencyQuery); err != nil {
		return nil, err
	}

	// Calculate the histogram based on the extracted features.
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(hostResidency VMHostResidencyRaw) []string {
		return []string{hostResidency.FlavorName, "all"}
	}
	valueFunc := func(hostResidency VMHostResidencyRaw) float64 {
		return float64(hostResidency.Duration)
	}
	hists, counts, sums := tools.Histogram(hostResidencies, buckets, keysFunc, valueFunc)
	var features []shared.VMHostResidencyHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			slog.Warn("vm_host_residency: unexpected comma in flavor name")
			continue
		}
		for bucket, value := range hist {
			// Create a feature for each bucket.
			features = append(features, shared.VMHostResidencyHistogramBucket{
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

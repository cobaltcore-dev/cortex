// Copyright SAP SE
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

// Feature that describes how long a vm was running on a host until it needed
// to move out, and the reason for the move (i.e., who forced it to move).
type VMHostResidencyHistogramBucket struct {
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
	// The bucket this residency falls into.
	Bucket float64 `db:"bucket"`
	// The value of the bucket.
	Value uint64 `db:"value"`
	// The count of vms that fell into this bucket.
	Count uint64 `db:"count"`
	// The sum of all durations that fell into this bucket.
	Sum float64 `db:"sum"`
}

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                       // No options passed through yaml config
		VMHostResidencyHistogramBucket, // Feature model
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
	var features []VMHostResidencyHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			slog.Warn("vm_host_residency: unexpected comma in flavor name")
			continue
		}
		for bucket, value := range hist {
			// Create a feature for each bucket.
			features = append(features, VMHostResidencyHistogramBucket{
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

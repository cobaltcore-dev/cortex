// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/math"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
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
	extractor.BaseExtractor[struct{}]
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
	hists, counts, sums := math.Histogram(lifeSpans, buckets, keysFunc, valueFunc)
	var features []VMLifeSpanHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			log.V(1).Info("vm_life_span: unexpected comma in flavor name")
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

// Inputs of the vm life span extractor.
type VMLifeSpanInputs struct {
	Servers        []nova.Server        `input:"servers"`
	DeletedServers []nova.DeletedServer `input:"deletedServers"`
	Flavors        []nova.Flavor        `input:"flavors"`
}

// Calculate the life span of deleted servers from creation until their last
// update, and of running servers from creation until now. Servers with
// invalid timestamps are skipped. Flavors are "unknown" if they don't exist.
func vmLifeSpans(in VMLifeSpanInputs, now time.Time) []VMLifeSpanRaw {
	flavorNames := make(map[string]struct{}, len(in.Flavors))
	for _, f := range in.Flavors {
		flavorNames[f.Name] = struct{}{}
	}
	flavorName := func(name string) string {
		if _, ok := flavorNames[name]; ok {
			return name
		}
		return "unknown"
	}
	lifeSpans := make([]VMLifeSpanRaw, 0, len(in.Servers)+len(in.DeletedServers))
	for _, s := range in.DeletedServers {
		created, err := parseTimestamp(s.Created)
		if err != nil {
			continue
		}
		updated, err := parseTimestamp(s.Updated)
		if err != nil {
			continue
		}
		lifeSpans = append(lifeSpans, VMLifeSpanRaw{
			Duration:   secondsBetween(created, updated),
			FlavorName: flavorName(s.FlavorName),
			Deleted:    true,
		})
	}
	for _, s := range in.Servers {
		created, err := parseTimestamp(s.Created)
		if err != nil {
			continue
		}
		lifeSpans = append(lifeSpans, VMLifeSpanRaw{
			Duration:   secondsBetween(created, now),
			FlavorName: flavorName(s.FlavorName),
			Deleted:    false,
		})
	}
	return lifeSpans
}

// Extract histograms of how long vms existed, by flavor and across all
// flavors, separately for deleted and running vms.
func (e *VMLifeSpanHistogramExtractor) Extract(_ context.Context, in VMLifeSpanInputs) ([]VMLifeSpanHistogramBucket, error) {
	lifeSpansRaw := vmLifeSpans(in, time.Now())
	features := slices.Concat(
		extractHistogramBuckets(lifeSpansRaw, true),
		extractHistogramBuckets(lifeSpansRaw, false),
	)
	slices.SortFunc(features, func(a, b VMLifeSpanHistogramBucket) int {
		return cmp.Or(
			cmp.Compare(a.FlavorName, b.FlavorName),
			compareBool(a.Deleted, b.Deleted),
			cmp.Compare(a.Bucket, b.Bucket),
		)
	})
	return features, nil
}

// Compare two booleans, ordering false first.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}

func init() {
	extractor.Register("vm_life_span_histogram_extractor", func() extractor.Extractor[VMLifeSpanInputs, VMLifeSpanHistogramBucket] {
		return &VMLifeSpanHistogramExtractor{}
	})
}

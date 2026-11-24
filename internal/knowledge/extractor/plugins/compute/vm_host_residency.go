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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("compute")

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

// Inputs of the vm host residency extractor.
type VMHostResidencyInputs struct {
	Migrations []nova.Migration `input:"migrations"`
	Servers    []nova.Server    `input:"servers"`
	Flavors    []nova.Flavor    `input:"flavors"`
}

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

type VMHostResidencyRaw struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
}

// Calculate how long the vm of each migration stayed on its host: since its
// previous migration, or since its creation if there is none. Durations are
// 0 and flavors "unknown" when they can't be determined, e.g. because the
// server vanished already.
func vmHostResidencies(in VMHostResidencyInputs) []VMHostResidencyRaw {
	servers := make(map[string]nova.Server, len(in.Servers))
	for _, s := range in.Servers {
		servers[s.ID] = s
	}
	flavorNames := make(map[string]struct{}, len(in.Flavors))
	for _, f := range in.Flavors {
		flavorNames[f.Name] = struct{}{}
	}
	type migration struct {
		nova.Migration
		createdAt time.Time
		valid     bool
	}
	byInstance := make(map[string][]migration)
	for _, m := range in.Migrations {
		createdAt, err := parseTimestamp(m.CreatedAt)
		byInstance[m.InstanceUUID] = append(byInstance[m.InstanceUUID], migration{m, createdAt, err == nil})
	}
	residencies := make([]VMHostResidencyRaw, 0, len(in.Migrations))
	for _, migrations := range byInstance {
		slices.SortStableFunc(migrations, func(a, b migration) int {
			return a.createdAt.Compare(b.createdAt)
		})
		for i, m := range migrations {
			residency := VMHostResidencyRaw{FlavorName: "unknown"}
			server, serverFound := servers[m.InstanceUUID]
			if _, ok := flavorNames[server.FlavorName]; serverFound && ok {
				residency.FlavorName = server.FlavorName
			}
			if m.valid {
				if i > 0 && migrations[i-1].valid {
					residency.Duration = secondsBetween(migrations[i-1].createdAt, m.createdAt)
				} else if serverFound {
					if created, err := parseTimestamp(server.Created); err == nil {
						residency.Duration = secondsBetween(created, m.createdAt)
					}
				}
			}
			residencies = append(residencies, residency)
		}
	}
	return residencies
}

// Extract a histogram of how long vms stayed on their hosts until they were
// migrated, by flavor and across all flavors.
func (e *VMHostResidencyExtractor) Extract(_ context.Context, in VMHostResidencyInputs) ([]VMHostResidencyHistogramBucket, error) {
	hostResidencies := vmHostResidencies(in)
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(hostResidency VMHostResidencyRaw) []string {
		return []string{hostResidency.FlavorName, "all"}
	}
	valueFunc := func(hostResidency VMHostResidencyRaw) float64 {
		return float64(hostResidency.Duration)
	}
	hists, counts, sums := math.Histogram(hostResidencies, buckets, keysFunc, valueFunc)
	var features []VMHostResidencyHistogramBucket
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			log.V(1).Info("vm_host_residency: unexpected comma in flavor name")
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
	slices.SortFunc(features, func(a, b VMHostResidencyHistogramBucket) int {
		return cmp.Or(cmp.Compare(a.FlavorName, b.FlavorName), cmp.Compare(a.Bucket, b.Bucket))
	})
	return features, nil
}

func init() {
	extractor.Register("vm_host_residency_extractor", func() extractor.Extractor[VMHostResidencyInputs, VMHostResidencyHistogramBucket] {
		return &VMHostResidencyExtractor{}
	})
}

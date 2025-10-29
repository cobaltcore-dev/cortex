// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

type VMLifeSpanRaw struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
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

// Table under which the feature is stored.
func (VMLifeSpanHistogramBucket) TableName() string {
	return "feature_vm_life_span_histogram_bucket_v2"
}

// Indexes for the feature.
func (VMLifeSpanHistogramBucket) Indexes() map[string][]string { return nil }

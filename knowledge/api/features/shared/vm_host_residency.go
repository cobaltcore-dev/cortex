// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

// Feature that describes how long a vm was running on a host until it needed
// to move out, and the reason for the move (i.e., who forced it to move).
type VMHostResidencyHistogramBucket struct {
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
}

// Table under which the feature is stored.
func (VMHostResidencyHistogramBucket) TableName() string {
	return "feature_vm_host_residency_histogram_bucket"
}

// Indexes for the feature.
func (VMHostResidencyHistogramBucket) Indexes() map[string][]string { return nil }

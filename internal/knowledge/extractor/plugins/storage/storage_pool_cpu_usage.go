// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package storage

// Feature that maps the cpu usage of a storage pool.
type StoragePoolCPUUsage struct {
	// Name of the OpenStack storage pool.
	StoragePoolName string `db:"storage_pool_name"`
	// Avg CPU usage in pct.
	AvgCPUUsagePct float64 `db:"avg_cpu_usage_pct"`
	// Max CPU usage in pct.
	MaxCPUUsagePct float64 `db:"max_cpu_usage_pct"`
}

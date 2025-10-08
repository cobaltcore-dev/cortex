// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

// Feature that maps memory active percentage of kvm hosts.
type NodeExporterHostMemoryActive struct {
	ComputeHost     string  `db:"compute_host"`
	AvgMemoryActive float64 `db:"avg_memory_active"`
	MaxMemoryActive float64 `db:"max_memory_active"`
}

// Table under which the feature is stored.
func (NodeExporterHostMemoryActive) TableName() string {
	return "feature_host_memory_active"
}

// Indexes for the feature.
func (NodeExporterHostMemoryActive) Indexes() map[string][]string {
	return nil
}

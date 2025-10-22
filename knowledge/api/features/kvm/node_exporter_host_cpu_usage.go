// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

// Feature that maps CPU usage of kvm hosts.
type NodeExporterHostCPUUsage struct {
	ComputeHost string  `db:"compute_host"`
	AvgCPUUsage float64 `db:"avg_cpu_usage"`
	MaxCPUUsage float64 `db:"max_cpu_usage"`
}

// Table under which the feature is stored.
func (NodeExporterHostCPUUsage) TableName() string {
	return "feature_host_cpu_usage"
}

// Indexes for the feature.
func (NodeExporterHostCPUUsage) Indexes() map[string][]string {
	return nil
}

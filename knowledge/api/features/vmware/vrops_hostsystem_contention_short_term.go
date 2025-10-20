// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

// Feature that maps CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTerm struct {
	ComputeHost      string  `db:"compute_host"`
	AvgCPUContention float64 `db:"avg_cpu_contention"`
	MaxCPUContention float64 `db:"max_cpu_contention"`
}

// Table under which the feature is stored.
func (VROpsHostsystemContentionShortTerm) TableName() string {
	return "feature_vrops_hostsystem_contention_short_term"
}

// Indexes for the feature.
func (VROpsHostsystemContentionShortTerm) Indexes() map[string][]string { return nil }

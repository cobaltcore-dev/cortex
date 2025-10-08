// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

// Feature that calculates the noisiness of projects and on which
// compute hosts they are currently running.
type VROpsProjectNoisiness struct {
	Project         string  `db:"project"`
	ComputeHost     string  `db:"compute_host"`
	AvgCPUOfProject float64 `db:"avg_cpu_of_project"`
}

// Table under which the feature is stored.
func (VROpsProjectNoisiness) TableName() string {
	return "feature_vrops_project_noisiness"
}

// Indexes for the feature.
func (VROpsProjectNoisiness) Indexes() map[string][]string {
	return map[string][]string{
		"idx_vrops_project_noisiness_project": {"project"},
	}
}

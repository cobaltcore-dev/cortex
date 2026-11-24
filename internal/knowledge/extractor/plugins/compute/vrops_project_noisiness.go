// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps the average cpu usage of a project to the compute hosts
// on which the project's vms are running.
type VROpsProjectNoisiness struct {
	Project         string  `db:"project"`
	ComputeHost     string  `db:"compute_host"`
	AvgCPUOfProject float64 `db:"avg_cpu_of_project"`
}

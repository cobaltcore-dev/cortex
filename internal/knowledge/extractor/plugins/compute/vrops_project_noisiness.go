// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// Feature that calculates the noisiness of projects and on which
// compute hosts they are currently running.
type VROpsProjectNoisiness struct {
	Project         string  `db:"project"`
	ComputeHost     string  `db:"compute_host"`
	AvgCPUOfProject float64 `db:"avg_cpu_of_project"`
}

// Extractor that extracts the noisiness of projects and on which compute
// hosts they are currently running and stores it as a feature into the database.
type VROpsProjectNoisinessExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},              // No options passed through yaml config
		VROpsProjectNoisiness, // Feature model
	]
}

//go:embed vrops_project_noisiness.sql
var vropsProjectNoisinessSQL string

func (e *VROpsProjectNoisinessExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsProjectNoisinessSQL)
}

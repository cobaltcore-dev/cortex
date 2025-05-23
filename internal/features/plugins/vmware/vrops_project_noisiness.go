// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

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
func (VROpsProjectNoisiness) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_vrops_project_noisiness_project",
			ColumnNames: []string{"project"},
		},
	}
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

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (e *VROpsProjectNoisinessExtractor) GetName() string {
	return "vrops_project_noisiness_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VROpsProjectNoisinessExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaHypervisorsSynced,
		prometheus.TriggerMetricAliasSynced("vrops_virtualmachine_cpu_demand_ratio"),
	}
}

//go:embed vrops_project_noisiness.sql
var vropsProjectNoisinessSQL string

func (e *VROpsProjectNoisinessExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsProjectNoisinessSQL)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts the noisiness of projects and on which compute
// hosts they are currently running and stores it as a feature into the database.
type VROpsProjectNoisinessExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                     // No options passed through yaml config
		vmware.VROpsProjectNoisiness, // Feature model
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

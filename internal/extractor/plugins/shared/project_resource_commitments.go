// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// Feature to calculate the total resource (vcpu, ram) commitments of all projects with commitments.
type ProjectResourceCommitments struct {
	// ID of the project.
	ProjectID string `db:"project_id,primarykey"`
	// Availability zone of the commitments.
	AvailabilityZone string `db:"availability_zone,primarykey"`
	// Total number of servers of the project.
	TotalInstanceCommitments int64 `db:"total_instance_commitments"`
	// Total number of servers of the project with a flavor that could not be resolved.
	UnresolvedInstanceCommitments int64 `db:"unresolved_instance_commitments"`
	// Total number of vcpus committed by a project.
	TotalVCPUsCommitted int64 `db:"total_committed_vcpus"`
	// Total amount of ram (in MB) committed by a project.
	TotalRAMCommittedMB int64 `db:"total_committed_ram_mb"`
	// Total amount of disk (in GB) committed by a project.
	TotalDiskCommittedGB int64 `db:"total_committed_disk_gb"`
}

// Table under which the feature is stored.
func (ProjectResourceCommitments) TableName() string {
	return "feature_project_resource_commitments_v2"
}

// Indexes for the feature.
func (ProjectResourceCommitments) Indexes() []db.Index { return nil }

// Extractor that extracts the resource commitments of all projects with commitments.
type ProjectResourceCommitmentsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                   // No options passed through yaml config
		ProjectResourceCommitments, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*ProjectResourceCommitmentsExtractor) GetName() string {
	return "project_resource_commitments_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (ProjectResourceCommitmentsExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaFlavorsSynced,
		identity.TriggerIdentityProjectsSynced,
		limes.TriggerLimesCommitmentsSynced,
	}
}

//go:embed project_resource_commitments.sql
var projectResourceCommitmentsQuery string

// Extract the resource commitments of all projects with commitments.
func (e *ProjectResourceCommitmentsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(projectResourceCommitmentsQuery)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
)

// Feature that describes how long a vm was running on a host until it needed
// to move out, and the reason for the move (i.e., who forced it to move).
type VMHostResidency struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor id of the virtual machine.
	FlavorID string `db:"flavor_id"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
	// The UUID of the virtual machine.
	InstanceUUID string `db:"instance_uuid"`
	// The migration uuid.
	MigrationUUID string `db:"migration_uuid"`
	// The host the VM was running on and needed to move out.
	SourceHost string `db:"source_host"`
	// The host the VM was moved to.
	TargetHost string `db:"target_host"`
	// The source node the VM was running on and needed to move out.
	SourceNode string `db:"source_node"`
	// The target node the VM was moved to.
	TargetNode string `db:"target_node"`
	// Who forced the VM to move out.
	UserID string `db:"user_id"`
	// To which project the user belongs that forced the VM to move out.
	ProjectID string `db:"project_id"`
	// Migration type (live-migration or resize).
	Type string `db:"type"`
	// Time when the migration was triggered in seconds since epoch.
	Time int `db:"time"`
}

// Table under which the feature is stored.
func (VMHostResidency) TableName() string {
	return "feature_vm_host_residency"
}

// Indexes for the feature.
func (VMHostResidency) Indexes() []db.Index {
	return nil
}

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},        // No options passed through yaml config
		VMHostResidency, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VMHostResidencyExtractor) GetName() string {
	return "vm_host_residency_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VMHostResidencyExtractor) Triggers() []string {
	return []string{
		openstack.TriggerNovaServersSynced,
		openstack.TriggerNovaMigrationsSynced,
		openstack.TriggerNovaFlavorsSynced,
	}
}

//go:embed vm_host_residency.sql
var vmHostResidencyQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMHostResidencyExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vmHostResidencyQuery)
}

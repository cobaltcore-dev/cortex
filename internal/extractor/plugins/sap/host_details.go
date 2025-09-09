// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
)

type HostDetails struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host.
	AvailabilityZone string `db:"availability_zone"`
	// CPU Architecture of the compute host.
	// Can be "cascade-lake" or "sapphire-rapids"
	CPUArchitecture string `db:"cpu_architecture"`
	// Hypervisor type of the compute host.
	HypervisorType string `db:"hypervisor_type"`
	// Hypervisor family of the compute host.
	// Can be "kvm" or "vmware"
	HypervisorFamily string `db:"hypervisor_family"`
	// Amount of VMs currently running on the compute host.
	RunningVMs int `db:"running_vms"`
	// Type of workload running on the compute host.
	// Can be "general-purpose" or "hana"
	WorkloadType string `db:"workload_type"`
	// Whether the compute host can be used for workloads.
	Enabled bool `db:"enabled"`
	// Reason why the compute host is disabled, if applicable.
	DisabledReason *string `db:"disabled_reason"`
	// Comma separated list of pinned projects of the ComputeHost.
	PinnedProjects *string `db:"pinned_projects"`
}

// Table under which the feature is stored.
func (HostDetails) TableName() string {
	return "feature_sap_host_details_v2"
}

// Indexes for the feature.
func (HostDetails) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_details_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

type HostDetailsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},    // No options passed through yaml config
		HostDetails, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostDetailsExtractor) GetName() string {
	return "sap_host_details_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostDetailsExtractor) Triggers() []string {
	return []string{
		placement.TriggerPlacementTraitsSynced,
		nova.TriggerNovaHypervisorsSynced,
	}
}

//go:embed host_details.sql
var hostDetailsQuery string

// Extract the traits of a compute host from the database.
func (e *HostDetailsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostDetailsQuery)
}

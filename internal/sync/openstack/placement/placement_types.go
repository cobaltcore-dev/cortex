// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Type alias for the OpenStack placement configuration.
type PlacementConf = conf.SyncOpenStackPlacementConfig

// Resource provider model from the OpenStack placement API.
// This model is returned when listing resource providers.
type ResourceProvider struct {
	UUID                       string `json:"uuid" db:"uuid,primarykey"`
	Name                       string `json:"name" db:"name"`
	ParentProviderUUID         string `json:"parent_provider_uuid" db:"parent_provider_uuid"`
	RootProviderUUID           string `json:"root_provider_uuid" db:"root_provider_uuid"`
	ResourceProviderGeneration int    `json:"resource_provider_generation" db:"resource_provider_generation"`
}

// Table in which the openstack model is stored.
func (r ResourceProvider) TableName() string { return "openstack_resource_providers" }

// Indexes for the resource provider table.
func (r ResourceProvider) Indexes() []db.Index { return nil }

// Resource provider trait model from the OpenStack placement API.
type Trait struct {
	// Corresponds to the hypervisor uuid in the nova hypervisors table.
	ResourceProviderUUID       string `db:"resource_provider_uuid,primarykey"`
	Name                       string `db:"name,primarykey"`
	ResourceProviderGeneration int    `db:"resource_provider_generation"`
}

// Table in which the openstack trait model is stored.
func (r Trait) TableName() string { return "openstack_resource_provider_traits" }

// Indexes for the resource provider trait table.
func (r Trait) Indexes() []db.Index { return nil }

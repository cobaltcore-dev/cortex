// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

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
func (r ResourceProvider) Indexes() map[string][]string { return nil }

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
func (r Trait) Indexes() map[string][]string { return nil }

// Combined model for resource provider inventories and usages.
//
// Both models are combined in one table to avoid frequent joins, since both are
// used in conjunction to calculate capacity. They are also fetched and displayed
// together when using the cli command `openstack resource provider inventory list`.
//
// See: https://docs.openstack.org/api-ref/placement/#list-resource-provider-inventories
// And: https://docs.openstack.org/api-ref/placement/#list-usages
type InventoryUsage struct {
	ResourceProviderUUID       string `db:"resource_provider_uuid,primarykey"`
	ResourceProviderGeneration int    `db:"resource_provider_generation"`

	// From the inventory API:

	// Something like: DISK_GB, VCPU, MEMORY_MB.
	InventoryClassName string `db:"inventory_class_name,primarykey"`
	// Overcommit factor for the inventory class.
	AllocationRatio float32 `db:"allocation_ratio"`
	// A maximum amount any single allocation against an inventory can have.
	MaxUnit int `db:"max_unit"`
	// A minimum amount any single allocation against an inventory can have.
	MinUnit int `db:"min_unit"`
	// The amount of the resource a provider has reserved for its own use.
	Reserved int `db:"reserved"`
	// A representation of the divisible amount of the resource that may be
	// requested. For example, step_size = 5 means that only values divisible by
	// 5 (5, 10, 15, etc.) can be requested.
	StepSize int `db:"step_size"`
	// The actual amount of the resource that the provider can accommodate.
	Total int `db:"total"`

	// From the inventory usage API:

	// The amount of the resource that is currently in use.
	Used int `db:"used"`
}

// Table in which the openstack inventory usage model is stored.
func (InventoryUsage) TableName() string { return "openstack_resource_provider_inventory_usages" }

// Indexes for the resource provider inventory table.
func (InventoryUsage) Indexes() map[string][]string { return nil }

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import "github.com/cobaltcore-dev/cortex/internal/db"

// Resource provider model from the OpenStack placement API.
// This model is returned when listing resource providers.
type ResourceProvider struct {
	UUID                       string `json:"uuid" db:"uuid,primarykey"`
	Name                       string `json:"name" db:"name"`
	ParentProviderUUID         string `json:"parent_provider_uuid" db:"parent_provider_uuid"`
	RootProviderUUID           string `json:"root_provider_uuid" db:"root_provider_uuid"`
	ResourceProviderGeneration int    `json:"resource_provider_generation" db:"resource_provider_generation"`
}

// Table in which the resource provider model is stored.
func (r ResourceProvider) TableName() string {
	return "openstack_resource_provider"
}

// Detail for a given resource provider from the OpenStack placement API.
// These models are returned when querying details on a single resource provider.
type ProviderDetail interface {
	db.Table
	// GetName returns the name of the OpenStack model.
	GetName() string
}

// Resource provider trait model from the OpenStack placement API.
type ResourceProviderTrait struct {
	ResourceProviderUUID       string `db:"resource_provider_uuid,primarykey"`
	Name                       string `db:"name,primarykey"`
	ResourceProviderGeneration int    `json:"resource_provider_generation" db:"resource_provider_generation"`
}

// Table in which the resource provider trait model is stored.
func (r ResourceProviderTrait) TableName() string {
	return "openstack_resource_provider_traits"
}

// GetName returns the name of the OpenStack model.
func (r ResourceProviderTrait) GetName() string {
	return "openstack_resource_provider_trait"
}

// Resource provider aggregate model from the OpenStack placement API.
type ResourceProviderAggregate struct {
	ResourceProviderUUID       string `db:"resource_provider_uuid,primarykey"`
	UUID                       string `db:"uuid,primarykey"`
	ResourceProviderGeneration int    `json:"resource_provider_generation" db:"resource_provider_generation"`
}

// Table in which the resource provider aggregate model is stored.
func (r ResourceProviderAggregate) TableName() string {
	return "openstack_resource_provider_aggregates"
}

// GetName returns the name of the OpenStack model.
func (r ResourceProviderAggregate) GetName() string {
	return "openstack_resource_provider_aggregate"
}

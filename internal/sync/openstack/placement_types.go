// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

type ResourceProvider struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName                  struct{} `pg:"openstack_resource_providers"`
	UUID                       string   `json:"uuid" pg:"uuid,notnull,pk"`
	Name                       string   `json:"name" pg:"name"`
	ParentProviderUUID         string   `json:"parent_provider_uuid" pg:"parent_provider_uuid"`
	RootProviderUUID           string   `json:"root_provider_uuid" pg:"root_provider_uuid"`
	ResourceProviderGeneration int      `json:"resource_provider_generation" pg:"resource_provider_generation"`
}

type ProviderDetail interface {
	// GetName returns the name of the OpenStack model.
	GetName() string
	// Get the primary key of the model.
	GetPKField() string
}

type ResourceProviderTrait struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName                  struct{} `pg:"openstack_resource_provider_traits"`
	ResourceProviderUUID       string   `pg:"resource_provider_uuid,pk"`
	Name                       string   `pg:"name,pk"`
	ResourceProviderGeneration int      `json:"resource_provider_generation" pg:"resource_provider_generation"`
}

func (r ResourceProviderTrait) GetName() string    { return "openstack_resource_provider_trait" }
func (r ResourceProviderTrait) GetPKField() string { return "resource_provider_uuid,name" }

type ResourceProviderAggregate struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName                  struct{} `pg:"openstack_resource_provider_aggregates"`
	ResourceProviderUUID       string   `pg:"resource_provider_uuid,pk"`
	UUID                       string   `pg:"uuid,pk"`
	ResourceProviderGeneration int      `json:"resource_provider_generation" pg:"resource_provider_generation"`
}

func (r ResourceProviderAggregate) GetName() string    { return "openstack_resource_provider_aggregate" }
func (r ResourceProviderAggregate) GetPKField() string { return "resource_provider_uuid,uuid" }

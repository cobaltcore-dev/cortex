// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

type ResourceProvider struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName          struct{} `pg:"openstack_resource_providers"`
	UUID               string   `json:"uuid" pg:"uuid,notnull,pk"`
	Name               string   `json:"name" pg:"name"`
	ParentProviderUUID string   `json:"parent_provider_uuid" pg:"parent_provider_uuid"`
	RootProviderUUID   string   `json:"root_provider_uuid" pg:"root_provider_uuid"`
}

type ResourceProviderTrait struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName            struct{} `pg:"openstack_resource_provider_traits"`
	ResourceProviderUUID string   `pg:"resource_provider_uuid"`
	Name                 string   `pg:"name,notnull,pk"`
}

type ResourceProviderAggregate struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName            struct{} `pg:"openstack_resource_provider_aggregates"`
	ResourceProviderUUID string   `pg:"resource_provider_uuid"`
	UUID                 string   `pg:"uuid,notnull,pk"`
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"encoding/json"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

// Type alias for the OpenStack Cinder configuration.
type CinderConf = conf.SyncOpenStackCinderConfig

// See https://docs.openstack.org/api-ref/block-storage/v3/#list-all-back-end-storage-pools
// Some fields are omitted.
type StoragePool struct {
	Name string `json:"name" db:"name,primarykey"`

	// Shared capabilities
	CapabilitiesAllocatedCapacityGB      float64 `json:"-" db:"capabilities_allocated_capacity_gb"`
	CapabilitiesDriverVersion            string  `json:"-" db:"capabilities_driver_version"`
	CapabilitiesFreeCapacityGB           float64 `json:"-" db:"capabilities_free_capacity_gb"`
	CapabilitiesMultiattach              bool    `json:"-" db:"capabilities_multiattach"`
	CapabilitiesPoolName                 string  `json:"-" db:"capabilities_pool_name"`
	CapabilitiesReservedPercentage       float64 `json:"-" db:"capabilities_reserved_percentage"`
	CapabilitiesStorageProtocol          string  `json:"-" db:"capabilities_storage_protocol"`
	CapabilitiesThickProvisioningSupport bool    `json:"-" db:"capabilities_thick_provisioning_support"`
	CapabilitiesThinProvisioningSupport  bool    `json:"-" db:"capabilities_thin_provisioning_support"`
	CapabilitiesTimestamp                string  `json:"-" db:"capabilities_timestamp"`
	CapabilitiesTotalCapacityGB          float64 `json:"-" db:"capabilities_total_capacity_gb"`
	CapabilitiesVendorName               string  `json:"-" db:"capabilities_vendor_name"`
	CapabilitiesVolumeBackendName        string  `json:"-" db:"capabilities_volume_backend_name"`

	// VMware specific fields
	CapabilitiesBackendState                     *string `json:"-" db:"capabilities_backend_state"`
	CapabilitiesCustomAttributeCinderState       *string `json:"-" db:"capabilities_custom_attribute_cinder_state"`
	CapabilitiesCustomAttributeCinderAggregateID *string `json:"-" db:"capabilities_custom_attribute_cinder_aggregate_id"`
	CapabilitiesCustomAttributeNetAppFQDN        *string `json:"-" db:"capabilities_custom_attribute_netapp_fqdn"`
	CapabilitiesPoolDownReason                   *string `json:"-" db:"capabilities_pool_down_reason"`
	CapabilitiesPoolState                        *string `json:"-" db:"capabilities_pool_state"`

	// NetApp-specific fields for native NetApp pools
	CapabilitiesNetAppAggregate            *string  `json:"-" db:"capabilities_netapp_aggregate"`
	CapabilitiesNetAppAggregateUsedPercent *float64 `json:"-" db:"capabilities_netapp_aggregate_used_percent"`
	CapabilitiesUtilization                *float64 `json:"-" db:"capabilities_utilization"`
}

// The table name for the storage pool model.
func (StoragePool) TableName() string { return "openstack_cinder_storage_pools" }

// Index for the openstack model.
func (StoragePool) Indexes() map[string][]string { return nil }

// Custom unmarshaler for StoragePool to handle nested JSON.
func (sp *StoragePool) UnmarshalJSON(data []byte) error {
	type Alias StoragePool
	aux := &struct {
		Capabilities json.RawMessage `json:"capabilities"`
		*Alias
	}{
		Alias: (*Alias)(sp),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var capabilities struct {
		AllocatedCapacityGB      float64 `json:"allocated_capacity_gb"`
		DriverVersion            string  `json:"driver_version"`
		FreeCapacityGB           float64 `json:"free_capacity_gb"`
		Multiattach              bool    `json:"multiattach"`
		PoolName                 string  `json:"pool_name"`
		ReservedPercentage       float64 `json:"reserved_percentage"`
		StorageProtocol          string  `json:"storage_protocol"`
		ThickProvisioningSupport bool    `json:"thick_provisioning_support"`
		ThinProvisioningSupport  bool    `json:"thin_provisioning_support"`
		Timestamp                string  `json:"timestamp"`
		TotalCapacityGB          float64 `json:"total_capacity_gb"`
		VendorName               string  `json:"vendor_name"`
		VolumeBackendName        string  `json:"volume_backend_name"`

		// VMware specific fields
		BackendState     *string `json:"backend_state"`
		CustomAttributes struct {
			CinderState       *string `json:"cinder_state"`
			CinderAggregateID *string `json:"cinder_aggregate_id"`
			NetAppFQDN        *string `json:"netapp_fqdn"`
		} `json:"custom_attributes"`
		PoolDownReason *string `json:"pool_down_reason"`
		PoolState      *string `json:"pool_state"`

		// NetApp-specific fields for native NetApp pools
		NetAppAggregate            *[]string `json:"netapp_aggregate"`
		NetAppAggregateUsedPercent *float64  `json:"netapp_aggregate_used_percent"`
		Utilization                *float64  `json:"utilization"`
	}
	if err := json.Unmarshal(aux.Capabilities, &capabilities); err != nil {
		return err
	}
	sp.CapabilitiesAllocatedCapacityGB = capabilities.AllocatedCapacityGB
	sp.CapabilitiesCustomAttributeNetAppFQDN = capabilities.CustomAttributes.NetAppFQDN
	sp.CapabilitiesDriverVersion = capabilities.DriverVersion
	sp.CapabilitiesFreeCapacityGB = capabilities.FreeCapacityGB
	sp.CapabilitiesMultiattach = capabilities.Multiattach
	sp.CapabilitiesPoolName = capabilities.PoolName
	sp.CapabilitiesReservedPercentage = capabilities.ReservedPercentage
	sp.CapabilitiesStorageProtocol = capabilities.StorageProtocol
	sp.CapabilitiesThickProvisioningSupport = capabilities.ThickProvisioningSupport
	sp.CapabilitiesThinProvisioningSupport = capabilities.ThinProvisioningSupport
	sp.CapabilitiesTimestamp = capabilities.Timestamp
	sp.CapabilitiesTotalCapacityGB = capabilities.TotalCapacityGB
	sp.CapabilitiesVendorName = capabilities.VendorName
	sp.CapabilitiesVolumeBackendName = capabilities.VolumeBackendName

	// VMware specific fields
	sp.CapabilitiesBackendState = capabilities.BackendState
	sp.CapabilitiesCustomAttributeCinderAggregateID = capabilities.CustomAttributes.CinderAggregateID
	sp.CapabilitiesCustomAttributeNetAppFQDN = capabilities.CustomAttributes.NetAppFQDN
	sp.CapabilitiesCustomAttributeCinderState = capabilities.CustomAttributes.CinderState
	sp.CapabilitiesPoolDownReason = capabilities.PoolDownReason
	sp.CapabilitiesPoolState = capabilities.PoolState

	// NetApp-specific fields for native NetApp pools
	if capabilities.NetAppAggregate != nil {
		aggregateStr := strings.Join(*capabilities.NetAppAggregate, ",")
		sp.CapabilitiesNetAppAggregate = &aggregateStr
	}
	sp.CapabilitiesNetAppAggregateUsedPercent = capabilities.NetAppAggregateUsedPercent
	sp.CapabilitiesUtilization = capabilities.Utilization
	return nil
}

// Custom marshaler for StoragePool to handle nested JSON.
func (sp *StoragePool) MarshalJSON() ([]byte, error) {
	// Reconstruct the capabilities object

	var netappAggregate []string
	if sp.CapabilitiesNetAppAggregate != nil && *sp.CapabilitiesNetAppAggregate != "" {
		netappAggregate = strings.Split(*sp.CapabilitiesNetAppAggregate, ",")
	}

	capabilities := map[string]any{
		"allocated_capacity_gb":      sp.CapabilitiesAllocatedCapacityGB,
		"driver_version":             sp.CapabilitiesDriverVersion,
		"free_capacity_gb":           sp.CapabilitiesFreeCapacityGB,
		"multiattach":                sp.CapabilitiesMultiattach,
		"pool_name":                  sp.CapabilitiesPoolName,
		"reserved_percentage":        sp.CapabilitiesReservedPercentage,
		"storage_protocol":           sp.CapabilitiesStorageProtocol,
		"thick_provisioning_support": sp.CapabilitiesThickProvisioningSupport,
		"thin_provisioning_support":  sp.CapabilitiesThinProvisioningSupport,
		"timestamp":                  sp.CapabilitiesTimestamp,
		"total_capacity_gb":          sp.CapabilitiesTotalCapacityGB,
		"vendor_name":                sp.CapabilitiesVendorName,
		"volume_backend_name":        sp.CapabilitiesVolumeBackendName,

		// VMware specific fields
		"backend_state": sp.CapabilitiesBackendState,
		"custom_attributes": map[string]any{
			"cinder_state":        sp.CapabilitiesCustomAttributeCinderState,
			"cinder_aggregate_id": sp.CapabilitiesCustomAttributeCinderAggregateID,
			"netapp_fqdn":         sp.CapabilitiesCustomAttributeNetAppFQDN,
		},
		"pool_down_reason": sp.CapabilitiesPoolDownReason,
		"pool_state":       sp.CapabilitiesPoolState,

		// NetApp-specific fields for native NetApp pools
		"netapp_aggregate":              netappAggregate,
		"netapp_aggregate_used_percent": sp.CapabilitiesNetAppAggregateUsedPercent,
		"utilization":                   sp.CapabilitiesUtilization,
	}

	// Create the final structure with capabilities nested
	result := map[string]any{
		"name":         sp.Name,
		"capabilities": capabilities,
	}

	return json.Marshal(result)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"encoding/json"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Type alias for the OpenStack Cinder configuration.
type CinderConf = conf.SyncOpenStackCinderConfig

// TODO mention not all
// TODO check what happend to the 5 who dont support this schema and document it
type StoragePool struct {
	Name                                         string  `json:"name" db:"name,primarykey"`
	CapabilitiesAllocatedCapacityGB              float64 `json:"-" db:"capabilities_allocated_capacity_gb"`
	CapabilitiesBackendState                     string  `json:"-" db:"capabilities_backend_state"`
	CapabilitiesCustomAttributeCinderAggregateID string  `json:"-" db:"capabilities_custom_attribute_cinder_aggregate_id"`
	CapabilitiesCustomAttributeNetAppFQDN        string  `json:"-" db:"capabilities_custom_attribute_netapp_fqdn"`
	CapabilitiesDatastoreType                    string  `json:"-" db:"capabilities_datastore_type"`
	CapabilitiesDriverVersion                    string  `json:"-" db:"capabilities_driver_version"`
	CapabilitiesFreeCapacityGB                   float64 `json:"-" db:"capabilities_free_capacity_gb"`
	CapabilitiesMultiattach                      bool    `json:"-" db:"capabilities_multiattach"`
	CapabilitiesPoolDownReason                   string  `json:"-" db:"capabilities_pool_down_reason"`
	CapabilitiesPoolName                         string  `json:"-" db:"capabilities_pool_name"`
	CapabilitiesPoolState                        string  `json:"-" db:"capabilities_pool_state"`
	CapabilitiesQualityType                      string  `json:"-" db:"capabilities_quality_type"`
	CapabilitiesReservedPercentage               float64 `json:"-" db:"capabilities_reserved_percentage"`
	CapabilitiesStorageProfile                   string  `json:"-" db:"capabilities_storage_profile"`
	CapabilitiesStorageProtocol                  string  `json:"-" db:"capabilities_storage_protocol"`
	CapabilitiesThickProvisioningSupport         bool    `json:"-" db:"capabilities_thick_provisioning_support"`
	CapabilitiesThinProvisioningSupport          bool    `json:"-" db:"capabilities_thin_provisioning_support"`
	CapabilitiesTimestamp                        string  `json:"-" db:"capabilities_timestamp"`
	CapabilitiesTotalCapacityGB                  float64 `json:"-" db:"capabilities_total_capacity_gb"`
	CapabilitiesVCenterShard                     string  `json:"-" db:"capabilities_vcenter_shard"`
	CapabilitiesVendorName                       string  `json:"-" db:"capabilities_vendor_name"`
	CapabilitiesVolumeBackendName                string  `json:"-" db:"capabilities_volume_backend_name"`
}

// The table name for the storage pool model.
func (StoragePool) TableName() string { return "openstack_cinder_storage_pools" }

// Index for the openstack model.
func (StoragePool) Indexes() []db.Index { return nil }

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
		AllocatedCapacityGB float64 `json:"allocated_capacity_gb"`
		BackendState        string  `json:"backend_state"`
		CustomAttributes    struct {
			CinderAggregateID string `json:"cinder_aggregate_id"`
			NetAppFQDN        string `json:"netapp_fqdn"`
		} `json:"custom_attributes"`
		DatastoreType            string  `json:"datastore_type"`
		DriverVersion            string  `json:"driver_version"`
		FreeCapacityGB           float64 `json:"free_capacity_gb"`
		Multiattach              bool    `json:"multiattach"`
		PoolDownReason           string  `json:"pool_down_reason"`
		PoolName                 string  `json:"pool_name"`
		PoolState                string  `json:"pool_state"`
		QualityType              string  `json:"quality_type"`
		ReservedPercentage       float64 `json:"reserved_percentage"`
		StorageProfile           string  `json:"storage_profile"`
		StorageProtocol          string  `json:"storage_protocol"`
		ThickProvisioningSupport bool    `json:"thick_provisioning_support"`
		ThinProvisioningSupport  bool    `json:"thin_provisioning_support"`
		Timestamp                string  `json:"timestamp"`
		TotalCapacityGB          float64 `json:"total_capacity_gb"`
		VCenterShard             string  `json:"vcenter_shard"`
		VendorName               string  `json:"vendor_name"`
		VolumeBackendName        string  `json:"capabilities_volume_backend_name"`
	}
	if err := json.Unmarshal(aux.Capabilities, &capabilities); err != nil {
		return err
	}
	sp.CapabilitiesAllocatedCapacityGB = capabilities.AllocatedCapacityGB
	sp.CapabilitiesCustomAttributeNetAppFQDN = capabilities.CustomAttributes.NetAppFQDN
	sp.CapabilitiesBackendState = capabilities.BackendState
	sp.CapabilitiesCustomAttributeCinderAggregateID = capabilities.CustomAttributes.CinderAggregateID
	sp.CapabilitiesCustomAttributeNetAppFQDN = capabilities.CustomAttributes.NetAppFQDN
	sp.CapabilitiesDatastoreType = capabilities.DatastoreType
	sp.CapabilitiesDriverVersion = capabilities.DriverVersion
	sp.CapabilitiesFreeCapacityGB = capabilities.FreeCapacityGB
	sp.CapabilitiesMultiattach = capabilities.Multiattach
	sp.CapabilitiesPoolDownReason = capabilities.PoolDownReason
	sp.CapabilitiesPoolName = capabilities.PoolName
	sp.CapabilitiesPoolState = capabilities.PoolState
	sp.CapabilitiesQualityType = capabilities.QualityType
	sp.CapabilitiesReservedPercentage = capabilities.ReservedPercentage
	sp.CapabilitiesStorageProfile = capabilities.StorageProfile
	sp.CapabilitiesStorageProtocol = capabilities.StorageProtocol
	sp.CapabilitiesThickProvisioningSupport = capabilities.ThickProvisioningSupport
	sp.CapabilitiesThinProvisioningSupport = capabilities.ThinProvisioningSupport
	sp.CapabilitiesTimestamp = capabilities.Timestamp
	sp.CapabilitiesTotalCapacityGB = capabilities.TotalCapacityGB
	sp.CapabilitiesVCenterShard = capabilities.VCenterShard
	sp.CapabilitiesVendorName = capabilities.VendorName
	sp.CapabilitiesVolumeBackendName = capabilities.VolumeBackendName
	return nil
}

// Custom marshaler for StoragePool to handle nested JSON.
func (sp *StoragePool) MarshalJSON() ([]byte, error) {
	// Reconstruct the capabilities object
	capabilities := map[string]any{
		"allocated_capacity_gb": sp.CapabilitiesAllocatedCapacityGB,
		"backend_state":         sp.CapabilitiesBackendState,
		"custom_attributes": map[string]any{
			"cinder_aggregate_id": sp.CapabilitiesCustomAttributeCinderAggregateID,
			"netapp_fqdn":         sp.CapabilitiesCustomAttributeNetAppFQDN,
		},
		"datastore_type":             sp.CapabilitiesDatastoreType,
		"driver_version":             sp.CapabilitiesDriverVersion,
		"free_capacity_gb":           sp.CapabilitiesFreeCapacityGB,
		"multiattach":                sp.CapabilitiesMultiattach,
		"pool_down_reason":           sp.CapabilitiesPoolDownReason,
		"pool_name":                  sp.CapabilitiesPoolName,
		"pool_state":                 sp.CapabilitiesPoolState,
		"quality_type":               sp.CapabilitiesQualityType,
		"reserved_percentage":        sp.CapabilitiesReservedPercentage,
		"storage_profile":            sp.CapabilitiesStorageProfile,
		"storage_protocol":           sp.CapabilitiesStorageProtocol,
		"thick_provisioning_support": sp.CapabilitiesThickProvisioningSupport,
		"thin_provisioning_support":  sp.CapabilitiesThinProvisioningSupport,
		"timestamp":                  sp.CapabilitiesTimestamp,
		"total_capacity_gb":          sp.CapabilitiesTotalCapacityGB,
		"vcenter_shard":              sp.CapabilitiesVCenterShard,
		"vendor_name":                sp.CapabilitiesVendorName,
		"volume_backend_name":        sp.CapabilitiesVolumeBackendName,
	}

	// Create the final structure with capabilities nested
	result := map[string]any{
		"name":         sp.Name,
		"capabilities": capabilities,
	}

	return json.Marshal(result)
}

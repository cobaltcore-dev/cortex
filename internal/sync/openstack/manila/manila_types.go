// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"encoding/json"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Type alias for the OpenStack Manila configuration.
type ManilaConf = conf.SyncOpenStackManilaConfig

// OpenStack Manila storage pool.
// See: https://docs.openstack.org/api-ref/shared-file-system/#list-back-end-storage-pools-with-details
type StoragePool struct {
	Name    string `json:"name" db:"name,primarykey"`
	Host    string `json:"host" db:"host"`
	Backend string `json:"backend" db:"backend"`
	Pool    string `json:"pool" db:"pool"`

	// From nested capabilities json.
	CapabilitiesPoolName                       string  `json:"-" db:"capabilities_pool_name"`
	CapabilitiesTotalCapacityGB                float64 `json:"-" db:"capabilities_total_capacity_gb"`
	CapabilitiesFreeCapacityGB                 float64 `json:"-" db:"capabilities_free_capacity_gb"`
	CapabilitiesReservedPercentage             int     `json:"-" db:"capabilities_reserved_percentage"`
	CapabilitiesShareBackendName               string  `json:"-" db:"capabilities_share_backend_name"`
	CapabilitiesStorageProtocol                string  `json:"-" db:"capabilities_storage_protocol"`
	CapabilitiesVendorName                     string  `json:"-" db:"capabilities_vendor_name"`
	CapabilitiesDriverVersion                  string  `json:"-" db:"capabilities_driver_version"`
	CapabilitiesTimestamp                      string  `json:"-" db:"capabilities_timestamp"`
	CapabilitiesDriverHandlesShareServers      bool    `json:"-" db:"capabilities_driver_handles_share_servers"`
	CapabilitiesSnapshotSupport                bool    `json:"-" db:"capabilities_snapshot_support"`
	CapabilitiesCreateShareFromSnapshotSupport bool    `json:"-" db:"capabilities_create_share_from_snapshot_support"`
	CapabilitiesRevertToSnapshotSupport        bool    `json:"-" db:"capabilities_revert_to_snapshot_support"`
	CapabilitiesMountSnapshotSupport           bool    `json:"-" db:"capabilities_mount_snapshot_support"`
	CapabilitiesDedupe                         bool    `json:"-" db:"capabilities_dedupe"`
	CapabilitiesCompression                    bool    `json:"-" db:"capabilities_compression"`
	CapabilitiesReplicationType                *string `json:"-" db:"capabilities_replication_type"`
	CapabilitiesReplicationDomain              *string `json:"-" db:"capabilities_replication_domain"`
	CapabilitiesSGConsistentSnapshotSupport    string  `json:"-" db:"capabilities_sg_consistent_snapshot_support"`
	CapabilitiesIPv4Support                    bool    `json:"-" db:"capabilities_ipv4_support"`
	CapabilitiesIPv6Support                    bool    `json:"-" db:"capabilities_ipv6_support"`
}

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
		PoolName                       string  `json:"pool_name"`
		TotalCapacityGB                float64 `json:"total_capacity_gb"`
		FreeCapacityGB                 float64 `json:"free_capacity_gb"`
		ReservedPercentage             int     `json:"reserved_percentage"`
		ShareBackendName               string  `json:"share_backend_name"`
		StorageProtocol                string  `json:"storage_protocol"`
		VendorName                     string  `json:"vendor_name"`
		DriverVersion                  string  `json:"driver_version"`
		Timestamp                      string  `json:"timestamp"`
		DriverHandlesShareServers      bool    `json:"driver_handles_share_servers"`
		SnapshotSupport                bool    `json:"snapshot_support"`
		CreateShareFromSnapshotSupport bool    `json:"create_share_from_snapshot_support"`
		RevertToSnapshotSupport        bool    `json:"revert_to_snapshot_support"`
		MountSnapshotSupport           bool    `json:"mount_snapshot_support"`
		Dedupe                         bool    `json:"dedupe"`
		Compression                    bool    `json:"compression"`
		ReplicationType                *string `json:"replication_type"`
		ReplicationDomain              *string `json:"replication_domain"`
		SGConsistentSnapshotSupport    string  `json:"sg_consistent_snapshot_support"`
		IPv4Support                    bool    `json:"ipv4_support"`
		IPv6Support                    bool    `json:"ipv6_support"`
	}
	if err := json.Unmarshal(aux.Capabilities, &capabilities); err != nil {
		return err
	}
	sp.CapabilitiesPoolName = capabilities.PoolName
	sp.CapabilitiesTotalCapacityGB = capabilities.TotalCapacityGB
	sp.CapabilitiesFreeCapacityGB = capabilities.FreeCapacityGB
	sp.CapabilitiesReservedPercentage = capabilities.ReservedPercentage
	sp.CapabilitiesShareBackendName = capabilities.ShareBackendName
	sp.CapabilitiesStorageProtocol = capabilities.StorageProtocol
	sp.CapabilitiesVendorName = capabilities.VendorName
	sp.CapabilitiesDriverVersion = capabilities.DriverVersion
	sp.CapabilitiesTimestamp = capabilities.Timestamp
	sp.CapabilitiesDriverHandlesShareServers = capabilities.DriverHandlesShareServers
	sp.CapabilitiesSnapshotSupport = capabilities.SnapshotSupport
	sp.CapabilitiesCreateShareFromSnapshotSupport = capabilities.CreateShareFromSnapshotSupport
	sp.CapabilitiesRevertToSnapshotSupport = capabilities.RevertToSnapshotSupport
	sp.CapabilitiesMountSnapshotSupport = capabilities.MountSnapshotSupport
	sp.CapabilitiesDedupe = capabilities.Dedupe
	sp.CapabilitiesCompression = capabilities.Compression
	sp.CapabilitiesReplicationType = capabilities.ReplicationType
	sp.CapabilitiesReplicationDomain = capabilities.ReplicationDomain
	sp.CapabilitiesSGConsistentSnapshotSupport = capabilities.SGConsistentSnapshotSupport
	sp.CapabilitiesIPv4Support = capabilities.IPv4Support
	sp.CapabilitiesIPv6Support = capabilities.IPv6Support
	return nil
}

// Table in which the openstack model is stored.
func (StoragePool) TableName() string { return "openstack_storage_pools" }

// Index for the openstack model.
func (StoragePool) Indexes() []db.Index { return nil }

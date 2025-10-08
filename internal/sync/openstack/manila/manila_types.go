// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"encoding/json"

	"github.com/cobaltcore-dev/cortex/internal/conf"
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

	CapabilitiesTotalCapacityGB             float64 `json:"-" db:"capabilities_total_capacity_gb"`
	CapabilitiesFreeCapacityGB              float64 `json:"-" db:"capabilities_free_capacity_gb"`
	CapabilitiesReservedPercentage          int     `json:"-" db:"capabilities_reserved_percentage"`
	CapabilitiesPoolName                    string  `json:"-" db:"capabilities_pool_name"`
	CapabilitiesShareBackendName            string  `json:"-" db:"capabilities_share_backend_name"`
	CapabilitiesStorageProtocol             string  `json:"-" db:"capabilities_storage_protocol"`
	CapabilitiesVendorName                  string  `json:"-" db:"capabilities_vendor_name"`
	CapabilitiesReplicationDomain           *string `json:"-" db:"capabilities_replication_domain"`
	CapabilitiesSGConsistentSnapshotSupport string  `json:"-" db:"capabilities_sg_consistent_snapshot_support"`
	CapabilitiesTimestamp                   string  `json:"-" db:"capabilities_timestamp"`

	// Fields that may be lists or single values -> json strings.

	CapabilitiesDriverVersion                  *string `json:"-" db:"capabilities_driver_version"`
	CapabilitiesReplicationType                *string `json:"-" db:"capabilities_replication_type"`
	CapabilitiesDriverHandlesShareServers      *string `json:"-" db:"capabilities_driver_handles_share_servers"`
	CapabilitiesSnapshotSupport                *string `json:"-" db:"capabilities_snapshot_support"`
	CapabilitiesCreateShareFromSnapshotSupport *string `json:"-" db:"capabilities_create_share_from_snapshot_support"`
	CapabilitiesRevertToSnapshotSupport        *string `json:"-" db:"capabilities_revert_to_snapshot_support"`
	CapabilitiesMountSnapshotSupport           *string `json:"-" db:"capabilities_mount_snapshot_support"`
	CapabilitiesDedupe                         *string `json:"-" db:"capabilities_dedupe"`
	CapabilitiesCompression                    *string `json:"-" db:"capabilities_compression"`
	CapabilitiesIPv4Support                    *string `json:"-" db:"capabilities_ipv4_support"`
	CapabilitiesIPv6Support                    *string `json:"-" db:"capabilities_ipv6_support"`
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
		TotalCapacityGB             float64 `json:"total_capacity_gb"`
		FreeCapacityGB              float64 `json:"free_capacity_gb"`
		ReservedPercentage          int     `json:"reserved_percentage"`
		PoolName                    string  `json:"pool_name"`
		ShareBackendName            string  `json:"share_backend_name"`
		StorageProtocol             string  `json:"storage_protocol"`
		VendorName                  string  `json:"vendor_name"`
		ReplicationDomain           *string `json:"replication_domain"`
		SGConsistentSnapshotSupport string  `json:"sg_consistent_snapshot_support"`
		Timestamp                   string  `json:"timestamp"`

		// Fields that may be lists or single values.

		DriverVersion                  any `json:"driver_version"`
		ReplicationType                any `json:"replication_type"`
		DriverHandlesShareServers      any `json:"driver_handles_share_servers"`
		SnapshotSupport                any `json:"snapshot_support"`
		CreateShareFromSnapshotSupport any `json:"create_share_from_snapshot_support"`
		RevertToSnapshotSupport        any `json:"revert_to_snapshot_support"`
		MountSnapshotSupport           any `json:"mount_snapshot_support"`
		Dedupe                         any `json:"dedupe"`
		Compression                    any `json:"compression"`
		IPv4Support                    any `json:"ipv4_support"`
		IPv6Support                    any `json:"ipv6_support"`
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
	sp.CapabilitiesTimestamp = capabilities.Timestamp
	sp.CapabilitiesReplicationDomain = capabilities.ReplicationDomain
	sp.CapabilitiesSGConsistentSnapshotSupport = capabilities.SGConsistentSnapshotSupport

	parse := func(field **string, value any) error {
		if value == nil {
			*field = nil
			return nil
		}
		jsonValue, err := json.Marshal(value)
		if err != nil {
			return err
		}
		strValue := string(jsonValue)
		*field = &strValue
		return nil
	}
	fields := []struct {
		field **string
		value any
	}{
		{&sp.CapabilitiesDriverVersion, capabilities.DriverVersion},
		{&sp.CapabilitiesReplicationType, capabilities.ReplicationType},
		{&sp.CapabilitiesDriverHandlesShareServers, capabilities.DriverHandlesShareServers},
		{&sp.CapabilitiesSnapshotSupport, capabilities.SnapshotSupport},
		{&sp.CapabilitiesCreateShareFromSnapshotSupport, capabilities.CreateShareFromSnapshotSupport},
		{&sp.CapabilitiesRevertToSnapshotSupport, capabilities.RevertToSnapshotSupport},
		{&sp.CapabilitiesMountSnapshotSupport, capabilities.MountSnapshotSupport},
		{&sp.CapabilitiesDedupe, capabilities.Dedupe},
		{&sp.CapabilitiesCompression, capabilities.Compression},
		{&sp.CapabilitiesIPv4Support, capabilities.IPv4Support},
		{&sp.CapabilitiesIPv6Support, capabilities.IPv6Support},
	}
	for _, f := range fields {
		if err := parse(f.field, f.value); err != nil {
			return err
		}
	}
	return nil
}

// Custom marshaler for StoragePool to handle nested JSON.
func (sp *StoragePool) MarshalJSON() ([]byte, error) {
	// Helper function to parse JSON string back to original value
	parseJSONString := func(jsonStr *string) (any, error) {
		if jsonStr == nil {
			return nil, nil
		}
		var value any
		if err := json.Unmarshal([]byte(*jsonStr), &value); err != nil {
			return nil, err
		}
		return value, nil
	}

	// Parse all the JSON string fields back to their original values
	driverVersion, err := parseJSONString(sp.CapabilitiesDriverVersion)
	if err != nil {
		return nil, err
	}
	replicationType, err := parseJSONString(sp.CapabilitiesReplicationType)
	if err != nil {
		return nil, err
	}
	driverHandlesShareServers, err := parseJSONString(sp.CapabilitiesDriverHandlesShareServers)
	if err != nil {
		return nil, err
	}
	snapshotSupport, err := parseJSONString(sp.CapabilitiesSnapshotSupport)
	if err != nil {
		return nil, err
	}
	createShareFromSnapshotSupport, err := parseJSONString(sp.CapabilitiesCreateShareFromSnapshotSupport)
	if err != nil {
		return nil, err
	}
	revertToSnapshotSupport, err := parseJSONString(sp.CapabilitiesRevertToSnapshotSupport)
	if err != nil {
		return nil, err
	}
	mountSnapshotSupport, err := parseJSONString(sp.CapabilitiesMountSnapshotSupport)
	if err != nil {
		return nil, err
	}
	dedupe, err := parseJSONString(sp.CapabilitiesDedupe)
	if err != nil {
		return nil, err
	}
	compression, err := parseJSONString(sp.CapabilitiesCompression)
	if err != nil {
		return nil, err
	}
	ipv4Support, err := parseJSONString(sp.CapabilitiesIPv4Support)
	if err != nil {
		return nil, err
	}
	ipv6Support, err := parseJSONString(sp.CapabilitiesIPv6Support)
	if err != nil {
		return nil, err
	}

	// Reconstruct the capabilities object
	capabilities := map[string]any{
		"total_capacity_gb":                  sp.CapabilitiesTotalCapacityGB,
		"free_capacity_gb":                   sp.CapabilitiesFreeCapacityGB,
		"reserved_percentage":                sp.CapabilitiesReservedPercentage,
		"pool_name":                          sp.CapabilitiesPoolName,
		"share_backend_name":                 sp.CapabilitiesShareBackendName,
		"storage_protocol":                   sp.CapabilitiesStorageProtocol,
		"vendor_name":                        sp.CapabilitiesVendorName,
		"replication_domain":                 sp.CapabilitiesReplicationDomain,
		"sg_consistent_snapshot_support":     sp.CapabilitiesSGConsistentSnapshotSupport,
		"timestamp":                          sp.CapabilitiesTimestamp,
		"driver_version":                     driverVersion,
		"replication_type":                   replicationType,
		"driver_handles_share_servers":       driverHandlesShareServers,
		"snapshot_support":                   snapshotSupport,
		"create_share_from_snapshot_support": createShareFromSnapshotSupport,
		"revert_to_snapshot_support":         revertToSnapshotSupport,
		"mount_snapshot_support":             mountSnapshotSupport,
		"dedupe":                             dedupe,
		"compression":                        compression,
		"ipv4_support":                       ipv4Support,
		"ipv6_support":                       ipv6Support,
	}

	// Create the final structure with capabilities nested
	result := map[string]any{
		"name":         sp.Name,
		"host":         sp.Host,
		"backend":      sp.Backend,
		"pool":         sp.Pool,
		"capabilities": capabilities,
	}

	return json.Marshal(result)
}

// Table in which the openstack model is stored.
func (StoragePool) TableName() string { return "openstack_manila_storage_pools" }

// Index for the openstack model.
func (StoragePool) Indexes() map[string][]string { return nil }

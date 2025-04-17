// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

// Type alias for the OpenStack nova configuration.
type NovaConf = conf.SyncOpenStackNovaConfig

// OpenStack server model as returned by the Nova API under /servers/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-servers-detailed
type Server struct {
	ID                             string  `json:"id" db:"id,primarykey"`
	Name                           string  `json:"name" db:"name"`
	Status                         string  `json:"status" db:"status"`
	TenantID                       string  `json:"tenant_id" db:"tenant_id"`
	UserID                         string  `json:"user_id" db:"user_id"`
	HostID                         string  `json:"hostId" db:"host_id"`
	Created                        string  `json:"created" db:"created"`
	Updated                        string  `json:"updated" db:"updated"`
	AccessIPv4                     string  `json:"accessIPv4" db:"access_ipv4"`
	AccessIPv6                     string  `json:"accessIPv6" db:"access_ipv6"`
	OSDCFdiskConfig                string  `json:"OS-DCF:diskConfig" db:"os_dcf_disk_config"`
	Progress                       int     `json:"progress" db:"progress"`
	OSEXTAvailabilityZone          string  `json:"OS-EXT-AZ:availability_zone" db:"os_ext_az_availability_zone"`
	ConfigDrive                    string  `json:"config_drive" db:"config_drive"`
	KeyName                        string  `json:"key_name" db:"key_name"`
	OSSRVUSGLaunchedAt             string  `json:"OS-SRV-USG:launched_at" db:"os_srv_usg_launched_at"`
	OSSRVUSGTerminatedAt           *string `json:"OS-SRV-USG:terminated_at" db:"os_srv_usg_terminated_at"`
	OSEXTSRVATTRHost               string  `json:"OS-EXT-SRV-ATTR:host" db:"os_ext_srv_attr_host"`
	OSEXTSRVATTRInstanceName       string  `json:"OS-EXT-SRV-ATTR:instance_name" db:"os_ext_srv_attr_instance_name"`
	OSEXTSRVATTRHypervisorHostname string  `json:"OS-EXT-SRV-ATTR:hypervisor_hostname" db:"os_ext_srv_attr_hypervisor_hostname"`
	OSEXTSTSTaskState              *string `json:"OS-EXT-STS:task_state" db:"os_ext_sts_task_state"`
	OSEXTSTSVmState                string  `json:"OS-EXT-STS:vm_state" db:"os_ext_sts_vm_state"`
	OSEXTSTSPowerState             int     `json:"OS-EXT-STS:power_state" db:"os_ext_sts_power_state"`

	// From nested JSON
	FlavorID string `json:"-" db:"flavor_id"`

	// Note: there are some more fields that are omitted. To include them again, add
	// custom unmarshalers and marshalers for the struct below.
}

// Custom unmarshaler for OpenStackServer to handle nested JSON.
func (s *Server) UnmarshalJSON(data []byte) error {
	type Alias Server
	aux := &struct {
		Flavor json.RawMessage `json:"flavor"`
		*Alias
	}{
		Alias: (*Alias)(s),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var flavor struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(aux.Flavor, &flavor); err != nil {
		return err
	}
	s.FlavorID = flavor.ID
	return nil
}

// Custom marshaler for OpenStackServer to handle nested JSON.
func (s *Server) MarshalJSON() ([]byte, error) {
	type Alias Server
	aux := &struct {
		Flavor struct {
			ID string `json:"id"`
		} `json:"flavor"`
		*Alias
	}{
		Alias: (*Alias)(s),
		Flavor: struct {
			ID string `json:"id"`
		}{
			ID: s.FlavorID,
		},
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (Server) TableName() string { return "openstack_servers" }

// OpenStack hypervisor model as returned by the Nova API under /os-hypervisors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-hypervisors-details
type Hypervisor struct {
	ID                int    `json:"id" db:"id,primarykey"`
	Hostname          string `json:"hypervisor_hostname" db:"hostname"`
	State             string `json:"state" db:"state"`
	Status            string `json:"status" db:"status"`
	HypervisorType    string `json:"hypervisor_type" db:"hypervisor_type"`
	HypervisorVersion int    `json:"hypervisor_version" db:"hypervisor_version"`
	HostIP            string `json:"host_ip" db:"host_ip"`
	// From nested JSON
	ServiceID             int     `json:"service_id" db:"service_id"`
	ServiceHost           string  `json:"service_host" db:"service_host"` // Used by the scheduler.
	ServiceDisabledReason *string `json:"service_disabled_reason" db:"service_disabled_reason"`
	VCPUs                 int     `json:"vcpus" db:"vcpus"`
	MemoryMB              int     `json:"memory_mb" db:"memory_mb"`
	LocalGB               int     `json:"local_gb" db:"local_gb"`
	VCPUsUsed             int     `json:"vcpus_used" db:"vcpus_used"`
	MemoryMBUsed          int     `json:"memory_mb_used" db:"memory_mb_used"`
	LocalGBUsed           int     `json:"local_gb_used" db:"local_gb_used"`
	FreeRAMMB             int     `json:"free_ram_mb" db:"free_ram_mb"`
	FreeDiskGB            int     `json:"free_disk_gb" db:"free_disk_gb"`
	CurrentWorkload       int     `json:"current_workload" db:"current_workload"`
	RunningVMs            int     `json:"running_vms" db:"running_vms"`
	DiskAvailableLeast    *int    `json:"disk_available_least" db:"disk_available_least"`
	CPUInfo               string  `json:"cpu_info" db:"cpu_info"`
}

// Custom unmarshaler for OpenStackHypervisor to handle nested JSON.
// Specifically, we unwrap the "service" field into separate fields.
// Flattening these fields makes querying the data easier.
func (h *Hypervisor) UnmarshalJSON(data []byte) error {
	type Alias Hypervisor
	aux := &struct {
		Service json.RawMessage `json:"service"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var service struct {
		ID             int     `json:"id"`
		Host           string  `json:"host"`
		DisabledReason *string `json:"disabled_reason"`
	}
	if err := json.Unmarshal(aux.Service, &service); err != nil {
		return err
	}
	h.ServiceID = service.ID
	h.ServiceHost = service.Host
	h.ServiceDisabledReason = service.DisabledReason
	return nil
}

// Custom marshaler for OpenStackHypervisor to handle nested JSON.
// Specifically, we wrap the "service" field into a separate JSON object.
// This is the reverse operation of the UnmarshalJSON method.
func (h *Hypervisor) MarshalJSON() ([]byte, error) {
	type Alias Hypervisor
	aux := &struct {
		Service json.RawMessage `json:"service"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	var service struct {
		ID             int     `json:"id"`
		Host           string  `json:"host"`
		DisabledReason *string `json:"disabled_reason"`
	}
	service.ID = h.ServiceID
	service.Host = h.ServiceHost
	service.DisabledReason = h.ServiceDisabledReason
	var err error
	aux.Service, err = json.Marshal(service)
	if err != nil {
		return nil, err
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (Hypervisor) TableName() string { return "openstack_hypervisors" }

// OpenStack flavor model as returned by the Nova API under /flavors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-flavors
type Flavor struct {
	ID          string  `json:"id" db:"id,primarykey"`
	Disk        int     `json:"disk" db:"disk"` // in GB.
	RAM         int     `json:"ram" db:"ram"`   // in MB.
	Name        string  `json:"name" db:"name"`
	RxTxFactor  float64 `json:"rxtx_factor" db:"rxtx_factor"`
	VCPUs       int     `json:"vcpus" db:"vcpus"`
	IsPublic    bool    `json:"os-flavor-access:is_public" db:"is_public"`
	Ephemeral   int     `json:"OS-FLV-EXT-DATA:ephemeral" db:"ephemeral"`
	Description string  `json:"description" db:"description"`
}

// Table in which the openstack model is stored.
func (Flavor) TableName() string { return "openstack_flavors" }

// OpenStack migration model as returned by the Nova API under /os-migrations.
// See: https://docs.openstack.org/api-ref/compute/#list-migrations
type Migration struct {
	ID                int    `json:"id" db:"id,primarykey"`
	UUID              string `json:"uuid" db:"uuid"`
	SourceCompute     string `json:"source_compute" db:"source_compute"`
	DestCompute       string `json:"dest_compute" db:"dest_compute"`
	SourceNode        string `json:"source_node" db:"source_node"`
	DestNode          string `json:"dest_node" db:"dest_node"`
	DestHost          string `json:"dest_host" db:"dest_host"`
	OldInstanceTypeID int    `json:"old_instance_type_id" db:"old_instance_type_id"`
	NewInstanceTypeID int    `json:"new_instance_type_id" db:"new_instance_type_id"`
	InstanceUUID      string `json:"instance_uuid" db:"instance_uuid"`
	Status            string `json:"status" db:"status"`
	MigrationType     string `json:"migration_type" db:"migration_type"`
	UserID            string `json:"user_id" db:"user_id"`
	ProjectID         string `json:"project_id" db:"project_id"`
	CreatedAt         string `json:"created_at" db:"created_at"`
	UpdatedAt         string `json:"updated_at" db:"updated_at"`
}

// Table in which the openstack model is stored.
func (Migration) TableName() string { return "openstack_migrations" }

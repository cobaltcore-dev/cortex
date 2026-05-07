// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"encoding/json"
	"log/slog"
)

// OpenStack server model as returned by the Nova API under /servers/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-servers-detailed
type DeletedServer struct {
	ID                             string  `json:"id" db:"id,primarykey"`
	Name                           string  `json:"name" db:"name"`
	Status                         string  `json:"status" db:"status"`
	TenantID                       string  `json:"tenant_id" db:"tenant_id"`
	UserID                         string  `json:"user_id" db:"user_id"`
	HostID                         string  `json:"hostId" db:"host_id"`
	Created                        string  `json:"created" db:"created"`
	Updated                        string  `json:"updated" db:"updated"`
	OSEXTAvailabilityZone          string  `json:"OS-EXT-AZ:availability_zone" db:"os_ext_az_availability_zone"`
	OSSRVUSGLaunchedAt             string  `json:"OS-SRV-USG:launched_at" db:"os_srv_usg_launched_at"`
	OSSRVUSGTerminatedAt           *string `json:"OS-SRV-USG:terminated_at" db:"os_srv_usg_terminated_at"`
	OSEXTSRVATTRHost               string  `json:"OS-EXT-SRV-ATTR:host" db:"os_ext_srv_attr_host"`
	OSEXTSRVATTRInstanceName       string  `json:"OS-EXT-SRV-ATTR:instance_name" db:"os_ext_srv_attr_instance_name"`
	OSEXTSRVATTRHypervisorHostname string  `json:"OS-EXT-SRV-ATTR:hypervisor_hostname" db:"os_ext_srv_attr_hypervisor_hostname"`

	// From nested JSON.
	FlavorName string `json:"-" db:"flavor_name"`

	// Note: there are some more fields that are omitted. To include them again, add
	// custom unmarshalers and marshalers for the struct below.
}

// Custom unmarshaler for OpenStackServer to handle nested JSON.
func (s *DeletedServer) UnmarshalJSON(data []byte) error {
	type Alias DeletedServer
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
		// Starting in microversion 2.47, "id" was removed...
		Name string `json:"original_name"`
	}
	if err := json.Unmarshal(aux.Flavor, &flavor); err != nil {
		return err
	}
	s.FlavorName = flavor.Name
	return nil
}

// Custom marshaler for OpenStackServer to handle nested JSON.
func (s *DeletedServer) MarshalJSON() ([]byte, error) {
	type Alias DeletedServer
	aux := &struct {
		Flavor struct {
			// Starting in microversion 2.47, "id" was removed...
			Name string `json:"original_name"`
		} `json:"flavor"`
		*Alias
	}{
		Alias: (*Alias)(s),
		Flavor: struct {
			Name string `json:"original_name"`
		}{
			Name: s.FlavorName,
		},
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (DeletedServer) TableName() string { return "openstack_deleted_servers" }

// Index for the openstack model.
func (DeletedServer) Indexes() map[string][]string { return nil }

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

	// From nested server.flavor JSON
	FlavorName string `json:"-" db:"flavor_name"`

	// ImageRef is the Glance image UUID the server was booted from.
	// Empty string for volume-booted servers.
	ImageRef string `json:"-" db:"image_ref"`

	// From nested server.fault JSON

	// The error response code.
	FaultCode *uint `json:"-" db:"fault_code"`
	// The date and time when the exception was raised. The date and time stamp
	// format is ISO 8601 (CCYY-MM-DDThh:mm:ss±hh:mm). For example,
	// 2015-08-27T09:49:58-05:00. The ±hh:mm value if included, is the time zone
	// as an offset from UTC. In the previous example, the offset value is -05:00.
	FaultCreated *string `json:"-" db:"fault_created"`
	// The error message.
	FaultMessage *string `json:"-" db:"fault_message"`
	// The stack trace. It is available if the response code is not 500 or you
	// have the administrator privilege.
	FaultDetails *string `json:"-" db:"fault_details"`

	// Note: there are some more fields that are omitted. To include them again, add
	// custom unmarshalers and marshalers for the struct below.
}

// Custom unmarshaler for OpenStackServer to handle nested JSON.
func (s *Server) UnmarshalJSON(data []byte) error {
	type Alias Server
	aux := &struct {
		Flavor json.RawMessage  `json:"flavor"`
		Fault  *json.RawMessage `json:"fault,omitempty"`
		// Nova returns image as a map {"id": "..."} for image-booted or "" for volume-booted.
		Image json.RawMessage `json:"image"`
		*Alias
	}{
		Alias: (*Alias)(s),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var flavor struct {
		// Starting in microversion 2.47, "id" was removed...
		Name string `json:"original_name"`
	}
	if err := json.Unmarshal(aux.Flavor, &flavor); err != nil {
		return err
	}
	s.FlavorName = flavor.Name
	// Parse image ref: map → extract id; empty string → leave blank (volume-booted).
	if len(aux.Image) > 0 && aux.Image[0] == '{' {
		var imageMap struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(aux.Image, &imageMap); err != nil {
			slog.Warn("failed to parse image ref from server response, leaving blank", "error", err, "serverID", s.ID)
		} else {
			s.ImageRef = imageMap.ID
		}
	}
	var fault struct {
		Code    uint    `json:"code"`
		Created string  `json:"created"`
		Message string  `json:"message"`
		Details *string `json:"details,omitempty"`
	}
	if aux.Fault != nil {
		if err := json.Unmarshal(*aux.Fault, &fault); err != nil {
			return err
		}
		s.FaultCode = &fault.Code
		s.FaultCreated = &fault.Created
		s.FaultMessage = &fault.Message
		s.FaultDetails = fault.Details
	}
	return nil
}

// Custom marshaler for OpenStackServer to handle nested JSON.
func (s *Server) MarshalJSON() ([]byte, error) {
	type Alias Server
	type flavor struct {
		// Starting in microversion 2.47, "id" was removed...
		Name string `json:"original_name"`
	}
	flavorVal := flavor{
		Name: s.FlavorName,
	}
	type fault struct {
		Code    uint    `json:"code"`
		Created string  `json:"created"`
		Message string  `json:"message"`
		Details *string `json:"details,omitempty"`
	}
	var faultVal *fault
	if s.FaultCode != nil && s.FaultCreated != nil && s.FaultMessage != nil {
		faultVal = &fault{
			Code:    *s.FaultCode,
			Created: *s.FaultCreated,
			Message: *s.FaultMessage,
			Details: s.FaultDetails,
		}
	}
	// Represent image as {"id": "<ref>"} for image-booted or "" for volume-booted.
	var imageVal any
	if s.ImageRef != "" {
		imageVal = map[string]string{"id": s.ImageRef}
	} else {
		imageVal = ""
	}
	aux := &struct {
		Flavor flavor `json:"flavor"`
		Fault  *fault `json:"fault,omitempty"`
		Image  any    `json:"image"`
		*Alias
	}{
		Alias:  (*Alias)(s),
		Flavor: flavorVal,
		Fault:  faultVal,
		Image:  imageVal,
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (Server) TableName() string { return "openstack_servers_v3" }

// Index for the openstack model.
func (Server) Indexes() map[string][]string { return nil }

// OpenStack hypervisor model as returned by the Nova API under /os-hypervisors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-hypervisors-details
type Hypervisor struct {
	ID                string `json:"id" db:"id,primarykey"`
	Hostname          string `json:"hypervisor_hostname" db:"hostname"`
	State             string `json:"state" db:"state"`
	Status            string `json:"status" db:"status"`
	HypervisorType    string `json:"hypervisor_type" db:"hypervisor_type"`
	HypervisorVersion int    `json:"hypervisor_version" db:"hypervisor_version"`
	HostIP            string `json:"host_ip" db:"host_ip"`
	// From nested JSON
	ServiceID             string  `json:"service_id" db:"service_id"`
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
		CPUInfo map[string]any  `json:"cpu_info"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var service struct {
		ID             string  `json:"id"`
		Host           string  `json:"host"`
		DisabledReason *string `json:"disabled_reason"`
	}
	if err := json.Unmarshal(aux.Service, &service); err != nil {
		return err
	}
	h.ServiceID = service.ID
	h.ServiceHost = service.Host
	h.ServiceDisabledReason = service.DisabledReason

	// Convert CPUInfo map to JSON string
	cpuInfoJSON, err := json.Marshal(aux.CPUInfo)
	if err != nil {
		return err
	}
	h.CPUInfo = string(cpuInfoJSON)
	return nil
}

// Custom marshaler for OpenStackHypervisor to handle nested JSON.
// Specifically, we wrap the "service" field into a separate JSON object.
// This is the reverse operation of the UnmarshalJSON method.
func (h *Hypervisor) MarshalJSON() ([]byte, error) {
	type Alias Hypervisor
	aux := &struct {
		Service json.RawMessage `json:"service"`
		CPUInfo map[string]any  `json:"cpu_info"` // Keep CPUInfo as a map[string]any
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	var service struct {
		ID             string  `json:"id"`
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
	// Ensure CPUInfo is a map[string]any
	// This is necessary to ensure that the CPUInfo field is stored as a JSON string in the database.
	aux.CPUInfo = make(map[string]any)
	if err := json.Unmarshal([]byte(h.CPUInfo), &aux.CPUInfo); err != nil {
		return nil, err
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (Hypervisor) TableName() string { return "openstack_hypervisors" }

// Index for the openstack model.
func (Hypervisor) Indexes() map[string][]string { return nil }

// OpenStack flavor model as returned by the Nova API under /flavors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-flavors
type Flavor struct {
	ID          string  `json:"id" db:"id,primarykey"`
	Disk        uint64  `json:"disk" db:"disk"` // in GB.
	RAM         uint64  `json:"ram" db:"ram"`   // in MB.
	Name        string  `json:"name" db:"name"`
	RxTxFactor  float64 `json:"rxtx_factor" db:"rxtx_factor"`
	VCPUs       uint64  `json:"vcpus" db:"vcpus"`
	IsPublic    bool    `json:"os-flavor-access:is_public" db:"is_public"`
	Ephemeral   uint64  `json:"OS-FLV-EXT-DATA:ephemeral" db:"ephemeral"`
	Description string  `json:"description" db:"description"`

	// JSON string of extra specifications used when scheduling the flavor.
	ExtraSpecs string `json:"extra_specs" db:"extra_specs"`
}

// FlavorHypervisorType is a type alias for a string to represent the specific
// values the hypervisor type contained in flavor extra specs may have.
type FlavorHypervisorType string

const (
	// FlavorHypervisorTypeQEMU maps a flavor for QEMU/KVM hypervisors.
	FlavorHypervisorTypeQEMU FlavorHypervisorType = "QEMU"
	// FlavorHypervisorTypeCH maps flavors to Cloud-Hypervisor/KVM hypervisors.
	FlavorHypervisorTypeCH FlavorHypervisorType = "CH"
	// FlavorHypervisorTypeVMware maps flavors to VMware hypervisors.
	FlavorHypervisorTypeVMware FlavorHypervisorType = "VMware vCenter Server"
	// FlavorHypervisorTypeIronic maps flavors to Ironic baremetal instances.
	FlavorHypervisorTypeIronic FlavorHypervisorType = "Ironic"
	// FlavorHypervisorTypeOther is a flavor for which the hypervisor type
	// is set in the extra specs but has an unknown value.
	FlavorHypervisorTypeOther FlavorHypervisorType = "Other"
	// FlavorHypervisorTypeUnspecified is a flavor for which the hypervisor type
	// is not set in the extra specs.
	FlavorHypervisorTypeUnspecified FlavorHypervisorType = "Unspecified"
)

// GetHypervisorType returns the hypervisor type of the flavor based on its
// extra specs.
func (f Flavor) GetHypervisorType() (FlavorHypervisorType, error) {
	var extraSpecs map[string]string
	if f.ExtraSpecs == "" {
		extraSpecs = map[string]string{}
	} else if err := json.Unmarshal([]byte(f.ExtraSpecs), &extraSpecs); err != nil {
		return "", err // Return an error if the extra specs cannot be parsed.
	}
	hypervisorType, ok := extraSpecs["capabilities:hypervisor_type"]
	if !ok {
		return FlavorHypervisorTypeUnspecified, nil
	}
	switch hypervisorType {
	case string(FlavorHypervisorTypeQEMU):
		return FlavorHypervisorTypeQEMU, nil
	case string(FlavorHypervisorTypeCH):
		return FlavorHypervisorTypeCH, nil
	case string(FlavorHypervisorTypeVMware):
		return FlavorHypervisorTypeVMware, nil
	case string(FlavorHypervisorTypeIronic):
		return FlavorHypervisorTypeIronic, nil
	default:
		return FlavorHypervisorTypeOther, nil
	}
}

// Custom unmarshaler for OpenStackFlavor to handle nested JSON.
func (f *Flavor) UnmarshalJSON(data []byte) error {
	type Alias Flavor
	aux := &struct {
		ExtraSpecs map[string]string `json:"extra_specs"`
		*Alias
	}{
		Alias: (*Alias)(f),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	f.ExtraSpecs = ""
	if len(aux.ExtraSpecs) > 0 {
		extraSpecsJSON, err := json.Marshal(aux.ExtraSpecs)
		if err != nil {
			return err
		}
		f.ExtraSpecs = string(extraSpecsJSON)
	}
	return nil
}

// Custom marshaler for OpenStackFlavor to handle nested JSON.
func (f *Flavor) MarshalJSON() ([]byte, error) {
	type Alias Flavor
	aux := &struct {
		ExtraSpecs map[string]string `json:"extra_specs"`
		*Alias
	}{
		Alias: (*Alias)(f),
	}
	if f.ExtraSpecs != "" {
		var extraSpecs map[string]string
		if err := json.Unmarshal([]byte(f.ExtraSpecs), &extraSpecs); err != nil {
			return nil, err
		}
		aux.ExtraSpecs = extraSpecs
	} else {
		aux.ExtraSpecs = make(map[string]string)
	}
	return json.Marshal(aux)
}

// Table in which the openstack model is stored.
func (Flavor) TableName() string { return "openstack_flavors_v2" }

// Index for the openstack model.
func (Flavor) Indexes() map[string][]string { return nil }

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

// Index for the openstack model.
func (Migration) Indexes() map[string][]string { return nil }

// Raw aggregate as returned by the Nova API under /os-aggregates.
type RawAggregate struct {
	UUID             string            `json:"uuid"`
	Name             string            `json:"name"`
	AvailabilityZone *string           `json:"availability_zone"`
	Hosts            []string          `json:"hosts"`
	Metadata         map[string]string `json:"metadata"`
}

// Aggregate as converted to be handled efficiently in a database.
type Aggregate struct {
	UUID             string  `json:"uuid" db:"uuid"`
	Name             string  `json:"name" db:"name"`
	AvailabilityZone *string `json:"availability_zone" db:"availability_zone"`
	ComputeHost      *string `json:"compute_host" db:"compute_host"` // Host that the aggregate is associated with.
	Metadata         string  `json:"metadata" db:"metadata"`         // JSON string of properties.
}

// Table in which the openstack model is stored.
func (Aggregate) TableName() string { return "openstack_aggregates_v2" }

// Index for the openstack model.
func (Aggregate) Indexes() map[string][]string { return nil }

// Image stores pre-computed os_type for a Glance image UUID.
// Populated by the NovaDatasourceTypeImages syncer from the Glance API.
// Used by the CR usage API to include os_type in VM subresources without live API calls.
type Image struct {
	ID     string `json:"id" db:"id,primarykey"`
	OSType string `json:"os_type" db:"os_type"`
}

// Table in which the openstack model is stored.
func (Image) TableName() string { return "openstack_images" }

// Index for the openstack model.
func (Image) Indexes() map[string][]string { return nil }

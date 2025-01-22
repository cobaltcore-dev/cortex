// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

// Paginated list response from the Nova API under /servers/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-servers-detailed
type openStackServerList struct {
	Servers []OpenStackServer `json:"servers"`
	// Pagination links.
	ServersLinks *[]struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"servers_links"`
}

// OpenStack server model as returned by the Nova API under /servers/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-servers-detailed
type OpenStackServer struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName                        struct{}        `pg:"openstack_servers"`
	ID                               string          `json:"id" pg:"id,notnull,pk"`
	Name                             string          `json:"name" pg:"name"`
	Status                           string          `json:"status" pg:"status"`
	TenantID                         string          `json:"tenant_id" pg:"tenant_id"`
	UserID                           string          `json:"user_id" pg:"user_id"`
	Metadata                         json.RawMessage `json:"metadata" pg:"metadata"`
	HostID                           string          `json:"hostId" pg:"host_id"`
	Image                            json.RawMessage `json:"image" pg:"image"`
	Created                          string          `json:"created" pg:"created"`
	Updated                          string          `json:"updated" pg:"updated"`
	Addresses                        json.RawMessage `json:"addresses" pg:"addresses"`
	AccessIPv4                       string          `json:"accessIPv4" pg:"access_ipv4"`
	AccessIPv6                       string          `json:"accessIPv6" pg:"access_ipv6"`
	Links                            json.RawMessage `json:"links" pg:"links"`
	OSDCFdiskConfig                  string          `json:"OS-DCF:diskConfig" pg:"os_dcf_disk_config"`
	Progress                         int             `json:"progress" pg:"progress"`
	OSEXTAvailabilityZone            string          `json:"OS-EXT-AZ:availability_zone" pg:"os_ext_az_availability_zone"`
	ConfigDrive                      string          `json:"config_drive" pg:"config_drive"`
	KeyName                          string          `json:"key_name" pg:"key_name"`
	OSSRVUSGLaunchedAt               string          `json:"OS-SRV-USG:launched_at" pg:"os_srv_usg_launched_at"`
	OSSRVUSGTerminatedAt             *string         `json:"OS-SRV-USG:terminated_at" pg:"os_srv_usg_terminated_at"`
	OSEXTSRVATTRHost                 string          `json:"OS-EXT-SRV-ATTR:host" pg:"os_ext_srv_attr_host"`
	OSEXTSRVATTRInstanceName         string          `json:"OS-EXT-SRV-ATTR:instance_name" pg:"os_ext_srv_attr_instance_name"`
	OSEXTSRVATTRHypervisorHostname   string          `json:"OS-EXT-SRV-ATTR:hypervisor_hostname" pg:"os_ext_srv_attr_hypervisor_hostname"`
	OSEXTSTSTaskState                *string         `json:"OS-EXT-STS:task_state" pg:"os_ext_sts_task_state"`
	OSEXTSTSVmState                  string          `json:"OS-EXT-STS:vm_state" pg:"os_ext_sts_vm_state"`
	OSEXTSTSPowerState               int             `json:"OS-EXT-STS:power_state" pg:"os_ext_sts_power_state"`
	OsExtendedVolumesVolumesAttached json.RawMessage `json:"os-extended-volumes:volumes_attached" pg:"os_extended_volumes_volumes_attached"`
	SecurityGroups                   json.RawMessage `json:"security_groups" pg:"security_groups"`
}

// Paginated list response from the Nova API under /os-hypervisors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-hypervisors-details
type openStackHypervisorList struct {
	Hypervisors []OpenStackHypervisor `json:"hypervisors"`
	// Pagination links.
	HypervisorsLinks *[]struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"hypervisors_links"`
}

// OpenStack hypervisor model as returned by the Nova API under /os-hypervisors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-hypervisors-details
type OpenStackHypervisor struct {
	//lint:ignore U1000 tableName is used by go-pg.
	tableName         struct{} `pg:"openstack_hypervisors"`
	ID                int      `json:"id" pg:"id,notnull,pk"`
	Hostname          string   `json:"hypervisor_hostname" pg:"hostname"`
	State             string   `json:"state" pg:"state"`
	Status            string   `json:"status" pg:"status"`
	HypervisorType    string   `json:"hypervisor_type" pg:"hypervisor_type"`
	HypervisorVersion int      `json:"hypervisor_version" pg:"hypervisor_version"`
	HostIP            string   `json:"host_ip" pg:"host_ip"`
	// From nested JSON
	ServiceID             int     `json:"service_id" pg:"service_id"`
	ServiceHost           string  `json:"service_host" pg:"service_host"` // Used by the scheduler.
	ServiceDisabledReason *string `json:"service_disabled_reason" pg:"service_disabled_reason"`
	VCPUs                 int     `json:"vcpus" pg:"vcpus"`
	MemoryMB              int     `json:"memory_mb" pg:"memory_mb"`
	LocalGB               int     `json:"local_gb" pg:"local_gb"`
	VCPUsUsed             int     `json:"vcpus_used" pg:"vcpus_used"`
	MemoryMBUsed          int     `json:"memory_mb_used" pg:"memory_mb_used"`
	LocalGBUsed           int     `json:"local_gb_used" pg:"local_gb_used"`
	FreeRAMMB             int     `json:"free_ram_mb" pg:"free_ram_mb"`
	FreeDiskGB            int     `json:"free_disk_gb" pg:"free_disk_gb"`
	CurrentWorkload       int     `json:"current_workload" pg:"current_workload"`
	RunningVMs            int     `json:"running_vms" pg:"running_vms"`
	DiskAvailableLeast    *int    `json:"disk_available_least" pg:"disk_available_least"`
	CPUInfo               string  `json:"cpu_info" pg:"cpu_info"`
}

// Custom unmarshaler for OpenStackHypervisor to handle nested JSON.
// Specifically, we unwrap the "service" field into separate fields.
// Flattening these fields makes querying the data easier.
func (h *OpenStackHypervisor) UnmarshalJSON(data []byte) error {
	type Alias OpenStackHypervisor
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
func (h *OpenStackHypervisor) MarshalJSON() ([]byte, error) {
	type Alias OpenStackHypervisor
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

type ServerAPI interface {
	Get(auth openStackKeystoneAuth, url *string) (*openStackServerList, error)
}

type serverAPI struct {
	monitor sync.Monitor
}

func NewServerAPI(monitor sync.Monitor) ServerAPI {
	return &serverAPI{monitor: monitor}
}

// GetServers returns a list of servers from the OpenStack Nova API.
// Note that this function may make multiple requests in case the returned
// data has multiple pages.
//
//nolint:dupl
func (api *serverAPI) Get(auth openStackKeystoneAuth, url *string) (*openStackServerList, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues("openstack_nova_servers")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// Use all_tenants=1 to get servers from all projects.
	var pageURL = auth.nova.URL + "servers/detail?all_tenants=1"
	if url != nil {
		pageURL = *url
	}
	logging.Log.Info("getting servers", "pageURL", pageURL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		logging.Log.Error("failed to create request", "error", err)
		return nil, err
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logging.Log.Error("failed to send request", "error", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logging.Log.Error("unexpected status code", "status", resp.StatusCode)
		return nil, err
	}
	var serverList openStackServerList
	err = json.NewDecoder(resp.Body).Decode(&serverList)
	if err != nil {
		logging.Log.Error("failed to decode response", "error", err)
		return nil, err
	}
	// If we got a paginated response, follow the next link.
	if serverList.ServersLinks != nil {
		for _, link := range *serverList.ServersLinks {
			if link.Rel == "next" {
				servers, err := api.Get(auth, &link.Href)
				if err != nil {
					return nil, err
				}
				serverList.Servers = append(serverList.Servers, servers.Servers...)
			}
		}
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_nova_servers").Inc()
	}
	return &serverList, nil
}

type HypervisorAPI interface {
	Get(auth openStackKeystoneAuth, url *string) (*openStackHypervisorList, error)
}

type hypervisorAPI struct {
	monitor sync.Monitor
}

func NewHypervisorAPI(monitor sync.Monitor) HypervisorAPI {
	return &hypervisorAPI{monitor: monitor}
}

// GetHypervisors returns a list of hypervisors from the OpenStack Nova API.
// Note that this function may make multiple requests in case the returned
// data has multiple pages.
//
//nolint:dupl
func (api *hypervisorAPI) Get(auth openStackKeystoneAuth, url *string) (*openStackHypervisorList, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues("openstack_nova_hypervisors")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var pageURL = auth.nova.URL + "os-hypervisors/detail"
	if url != nil {
		pageURL = *url
	}
	logging.Log.Info("getting hypervisors", "pageURL", pageURL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		logging.Log.Error("failed to create request", "error", err)
		return nil, err
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logging.Log.Error("failed to send request", "error", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logging.Log.Error("unexpected status code", "status", resp.StatusCode)
		return nil, err
	}
	var hypervisorList openStackHypervisorList
	err = json.NewDecoder(resp.Body).Decode(&hypervisorList)
	if err != nil {
		logging.Log.Error("failed to decode response", "error", err)
		return nil, err
	}
	// If we got a paginated response, follow the next link.
	if hypervisorList.HypervisorsLinks != nil {
		for _, link := range *hypervisorList.HypervisorsLinks {
			if link.Rel == "next" {
				hypervisors, err := api.Get(auth, &link.Href)
				if err != nil {
					return nil, err
				}
				hypervisorList.Hypervisors = append(hypervisorList.Hypervisors, hypervisors.Hypervisors...)
			}
		}
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_nova_hypervisors").Inc()
	}
	return &hypervisorList, nil
}

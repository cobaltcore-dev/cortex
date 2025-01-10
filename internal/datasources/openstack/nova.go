// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"log"
	"net/http"
)

type openStackServerList struct {
	Servers []OpenStackServer `json:"servers"`
}

type OpenStackServer struct {
	//lint:ignore U1000 Ignore unused field warning
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

type openStackHypervisorList struct {
	Hypervisors []OpenStackHypervisor `json:"hypervisors"`
}

type OpenStackHypervisor struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName         struct{} `pg:"openstack_hypervisors"`
	ID                int      `json:"id" pg:"id,notnull,pk"`
	Hostname          string   `json:"hypervisor_hostname" pg:"hostname"`
	State             string   `json:"state" pg:"state"`
	Status            string   `json:"status" pg:"status"`
	HypervisorType    string   `json:"hypervisor_type" pg:"hypervisor_type"`
	HypervisorVersion int      `json:"hypervisor_version" pg:"hypervisor_version"`
	HostIP            string   `json:"host_ip" pg:"host_ip"`
	Service           struct {
		ID             int     `json:"id" pg:"id"`
		Host           string  `json:"host" pg:"host"`
		DisabledReason *string `json:"disabled_reason" pg:"disabled_reason"`
	} `json:"service" pg:"service"`
	VCPUs              int    `json:"vcpus" pg:"vcpus"`
	MemoryMB           int    `json:"memory_mb" pg:"memory_mb"`
	LocalGB            int    `json:"local_gb" pg:"local_gb"`
	VCPUsUsed          int    `json:"vcpus_used" pg:"vcpus_used"`
	MemoryMBUsed       int    `json:"memory_mb_used" pg:"memory_mb_used"`
	LocalGBUsed        int    `json:"local_gb_used" pg:"local_gb_used"`
	FreeRAMMB          int    `json:"free_ram_mb" pg:"free_ram_mb"`
	FreeDiskGB         int    `json:"free_disk_gb" pg:"free_disk_gb"`
	CurrentWorkload    int    `json:"current_workload" pg:"current_workload"`
	RunningVMs         int    `json:"running_vms" pg:"running_vms"`
	DiskAvailableLeast *int   `json:"disk_available_least" pg:"disk_available_least"`
	CPUInfo            string `json:"cpu_info" pg:"cpu_info"`
}

func getServers(auth openStackKeystoneAuth) (openStackServerList, error) {
	req, err := http.NewRequest("GET", auth.nova.URL+"/servers/detail?all_tenants=1", nil)
	if err != nil {
		log.Fatalf("Failed to create server list request: %v", err)
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to get server list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to get server list, status code: %d", resp.StatusCode)
	}
	var serverList openStackServerList
	err = json.NewDecoder(resp.Body).Decode(&serverList)
	if err != nil {
		log.Fatalf("Failed to decode server list: %v", err)
	}
	return serverList, nil
}

func getHypervisors(auth openStackKeystoneAuth) (openStackHypervisorList, error) {
	req, err := http.NewRequest("GET", auth.nova.URL+"/os-hypervisors/detail", nil)
	if err != nil {
		log.Fatalf("Failed to create hypervisor list request: %v", err)
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to get hypervisor list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to get hypervisor list, status code: %d", resp.StatusCode)
	}
	var hypervisorList openStackHypervisorList
	err = json.NewDecoder(resp.Body).Decode(&hypervisorList)
	if err != nil {
		log.Fatalf("Failed to decode hypervisor list: %v", err)
	}
	return hypervisorList, nil
}

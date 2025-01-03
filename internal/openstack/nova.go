package openstack

import (
	"encoding/json"
	"log"
	"net/http"
)

type OpenStackServerList struct {
	Servers []OpenStackServer `json:"servers"`
}

type OpenStackServer struct {
	ID                               string            `json:"id"`
	Name                             string            `json:"name"`
	Status                           string            `json:"status"`
	TenantID                         string            `json:"tenant_id"`
	UserID                           string            `json:"user_id"`
	Metadata                         map[string]string `json:"metadata"`
	HostID                           string            `json:"hostId"`
	Image                            interface{}       `json:"image"`
	Flavor                           interface{}       `json:"flavor"`
	Created                          string            `json:"created"`
	Updated                          string            `json:"updated"`
	Addresses                        interface{}       `json:"addresses"`
	AccessIPv4                       string            `json:"accessIPv4"`
	AccessIPv6                       string            `json:"accessIPv6"`
	Links                            []interface{}     `json:"links"`
	OSDCFdiskConfig                  string            `json:"OS-DCF:diskConfig"`
	Progress                         int               `json:"progress"`
	OSEXTAvailabilityZone            string            `json:"OS-EXT-AZ:availability_zone"`
	ConfigDrive                      string            `json:"config_drive"`
	KeyName                          string            `json:"key_name"`
	OSSRVUSGLaunchedAt               string            `json:"OS-SRV-USG:launched_at"`
	OSSRVUSGTerminatedAt             *string           `json:"OS-SRV-USG:terminated_at"`
	OSEXTSRVATTRHost                 string            `json:"OS-EXT-SRV-ATTR:host"`
	OSEXTSRVATTRInstanceName         string            `json:"OS-EXT-SRV-ATTR:instance_name"`
	OSEXTSRVATTRHypervisorHostname   string            `json:"OS-EXT-SRV-ATTR:hypervisor_hostname"`
	OSEXTSTSTaskState                *string           `json:"OS-EXT-STS:task_state"`
	OSEXTSTSVmState                  string            `json:"OS-EXT-STS:vm_state"`
	OSEXTSTSPowerState               int               `json:"OS-EXT-STS:power_state"`
	OsExtendedVolumesVolumesAttached []interface{}     `json:"os-extended-volumes:volumes_attached"`
	SecurityGroups                   []interface{}     `json:"security_groups"`
}

type OpenStackHypervisorList struct {
	Hypervisors []OpenStackHypervisor `json:"hypervisors"`
}

type OpenStackHypervisor struct {
	ID                int    `json:"id"`
	Hostname          string `json:"hypervisor_hostname"`
	State             string `json:"state"`
	Status            string `json:"status"`
	HypervisorType    string `json:"hypervisor_type"`
	HypervisorVersion int    `json:"hypervisor_version"`
	HostIP            string `json:"host_ip"`
	Service           struct {
		ID             int     `json:"id"`
		Host           string  `json:"host"`
		DisabledReason *string `json:"disabled_reason"`
	} `json:"service"`
	VCPUs              int    `json:"vcpus"`
	MemoryMB           int    `json:"memory_mb"`
	LocalGB            int    `json:"local_gb"`
	VCPUsUsed          int    `json:"vcpus_used"`
	MemoryMBUsed       int    `json:"memory_mb_used"`
	LocalGBUsed        int    `json:"local_gb_used"`
	FreeRAMMB          int    `json:"free_ram_mb"`
	FreeDiskGB         int    `json:"free_disk_gb"`
	CurrentWorkload    int    `json:"current_workload"`
	RunningVMs         int    `json:"running_vms"`
	DiskAvailableLeast *int   `json:"disk_available_least"`
	CPUInfo            string `json:"cpu_info"`
}

func GetServers(auth OpenStackKeystoneAuth) (OpenStackServerList, error) {
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
	var serverList OpenStackServerList
	err = json.NewDecoder(resp.Body).Decode(&serverList)
	if err != nil {
		log.Fatalf("Failed to decode server list: %v", err)
	}
	return serverList, nil
}

func GetHypervisors(auth OpenStackKeystoneAuth) (OpenStackHypervisorList, error) {
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
	var hypervisorList OpenStackHypervisorList
	err = json.NewDecoder(resp.Body).Decode(&hypervisorList)
	if err != nil {
		log.Fatalf("Failed to decode hypervisor list: %v", err)
	}
	return hypervisorList, nil
}

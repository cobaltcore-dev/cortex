// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestUnmarshalOpenStackServer(t *testing.T) {
	data := []byte(`{
        "id": "server1",
        "name": "test-server",
        "status": "ACTIVE",
        "tenant_id": "tenant1",
        "user_id": "user1",
        "hostId": "host1",
        "created": "2025-01-01T00:00:00Z",
        "updated": "2025-01-02T00:00:00Z",
        "accessIPv4": "192.168.0.1",
        "accessIPv6": "fe80::1",
        "OS-DCF:diskConfig": "AUTO",
        "progress": 100,
        "OS-EXT-AZ:availability_zone": "nova",
        "config_drive": "True",
        "key_name": "key1",
        "OS-SRV-USG:launched_at": "2025-01-01T00:00:00Z",
        "OS-SRV-USG:terminated_at": null,
        "OS-EXT-SRV-ATTR:host": "host1",
        "OS-EXT-SRV-ATTR:instance_name": "instance1",
        "OS-EXT-SRV-ATTR:hypervisor_hostname": "hypervisor1",
        "OS-EXT-STS:task_state": null,
        "OS-EXT-STS:vm_state": "active",
        "OS-EXT-STS:power_state": 1,
        "flavor": {
            "id": "flavor1"
        }
    }`)

	var server Server
	err := json.Unmarshal(data, &server)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if server.ID != "server1" {
		t.Errorf("expected ID to be %s, got %s", "server1", server.ID)
	}
	if server.Name != "test-server" {
		t.Errorf("expected name to be %s, got %s", "test-server", server.Name)
	}
	if server.FlavorID != "flavor1" {
		t.Errorf("expected FlavorID to be %s, got %s", "flavor1", server.FlavorID)
	}
}

func TestMarshalOpenStackServer(t *testing.T) {
	server := Server{
		ID:                             "server1",
		Name:                           "test-server",
		Status:                         "ACTIVE",
		TenantID:                       "tenant1",
		UserID:                         "user1",
		HostID:                         "host1",
		Created:                        "2025-01-01T00:00:00Z",
		Updated:                        "2025-01-02T00:00:00Z",
		AccessIPv4:                     "192.168.0.1",
		AccessIPv6:                     "fe80::1",
		OSDCFdiskConfig:                "AUTO",
		Progress:                       100,
		OSEXTAvailabilityZone:          "nova",
		ConfigDrive:                    "True",
		KeyName:                        "key1",
		OSSRVUSGLaunchedAt:             "2025-01-01T00:00:00Z",
		OSSRVUSGTerminatedAt:           nil,
		OSEXTSRVATTRHost:               "host1",
		OSEXTSRVATTRInstanceName:       "instance1",
		OSEXTSRVATTRHypervisorHostname: "hypervisor1",
		OSEXTSTSTaskState:              nil,
		OSEXTSTSVmState:                "active",
		OSEXTSTSPowerState:             1,
		FlavorID:                       "flavor1",
	}

	data, err := json.Marshal(&server)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the data contains "flavor":
	if !json.Valid(data) {
		t.Error("expected valid JSON, got invalid")
	}
	fmt.Println(string(data))
	if !strings.Contains(string(data), `"flavor":{"id":"flavor1"}`) {
		t.Error("expected JSON to contain 'flavor' with 'id'")
	}
}

func TestUnmarshalOpenStackHypervisor(t *testing.T) {
	data := []byte(`{
        "id": 1,
        "hypervisor_hostname": "test-hypervisor",
        "state": "up",
        "status": "enabled",
        "hypervisor_type": "QEMU",
        "hypervisor_version": 1005003,
        "host_ip": "192.168.0.1",
        "service": {
            "id": 2,
            "host": "test-host",
            "disabled_reason": "maintenance"
        }
    }`)

	var hypervisor Hypervisor
	err := json.Unmarshal(data, &hypervisor)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if hypervisor.ID != 1 {
		t.Errorf("expected ID to be %d, got %d", 1, hypervisor.ID)
	}
	if hypervisor.Hostname != "test-hypervisor" {
		t.Errorf("expected hostname to be %s, got %s", "test-hypervisor", hypervisor.Hostname)
	}
	if hypervisor.ServiceID != 2 {
		t.Errorf("expected ServiceID to be %d, got %d", 2, hypervisor.ServiceID)
	}
	if hypervisor.ServiceHost != "test-host" {
		t.Errorf("expected ServiceHost to be %s, got %s", "test-host", hypervisor.ServiceHost)
	}
	if *hypervisor.ServiceDisabledReason != "maintenance" {
		t.Errorf("expected ServiceDisabledReason to be %s, got %s", "maintenance", *hypervisor.ServiceDisabledReason)
	}
}

func TestMarshalOpenStackHypervisor(t *testing.T) {
	disabledReason := "maintenance"
	hypervisor := Hypervisor{
		ID:                    1,
		Hostname:              "test-hypervisor",
		State:                 "up",
		Status:                "enabled",
		HypervisorType:        "QEMU",
		HypervisorVersion:     1005003,
		HostIP:                "192.168.0.1",
		ServiceID:             2,
		ServiceHost:           "test-host",
		ServiceDisabledReason: &disabledReason,
	}

	data, err := json.Marshal(hypervisor)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the data contains "service":
	if !json.Valid(data) {
		t.Error("expected valid JSON, got invalid")
	}
	if !strings.Contains(string(data), "service") {
		t.Error("expected JSON to contain 'service'")
	}
}

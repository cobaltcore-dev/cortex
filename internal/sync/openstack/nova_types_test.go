// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServerList_GetURL(t *testing.T) {
	list := ServerList{}
	expected := "servers/detail?all_tenants=1"
	if list.GetURL() != expected {
		t.Errorf("expected %s, got %s", expected, list.GetURL())
	}
}

func TestServerList_GetLinks(t *testing.T) {
	links := &[]PageLink{{Href: "http://example.com", Rel: "next"}}
	list := ServerList{ServersLinks: links}
	if list.GetLinks() != links {
		t.Errorf("expected %v, got %v", links, list.GetLinks())
	}
}

func TestHypervisorList_GetURL(t *testing.T) {
	list := HypervisorList{}
	expected := "os-hypervisors/detail"
	if list.GetURL() != expected {
		t.Errorf("expected %s, got %s", expected, list.GetURL())
	}
}

func TestHypervisorList_GetLinks(t *testing.T) {
	links := &[]PageLink{{Href: "http://example.com", Rel: "next"}}
	list := HypervisorList{HypervisorsLinks: links}
	if list.GetLinks() != links {
		t.Errorf("expected %v, got %v", links, list.GetLinks())
	}
}

func TestServer_GetName(t *testing.T) {
	server := Server{}
	expected := "openstack_server"
	if server.GetName() != expected {
		t.Errorf("expected %s, got %s", expected, server.GetName())
	}
}

func TestHypervisor_GetName(t *testing.T) {
	hypervisor := Hypervisor{}
	expected := "openstack_hypervisor"
	if hypervisor.GetName() != expected {
		t.Errorf("expected %s, got %s", expected, hypervisor.GetName())
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

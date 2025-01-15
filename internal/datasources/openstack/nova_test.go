// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetServers(t *testing.T) {
	// Mock the OpenStack Nova API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/servers/detail" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			//nolint:errcheck
			json.NewEncoder(w).Encode(openStackServerList{
				Servers: []OpenStackServer{
					{
						ID:   "server1",
						Name: "test-server",
					},
				},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	auth := openStackKeystoneAuth{
		nova: openStackEndpoint{
			URL: server.URL + "/",
		},
		token: "test-token",
	}

	// Call the function to test
	servers, err := getServers(auth, nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the results
	if len(servers.Servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(servers.Servers))
	}
	if servers.Servers[0].ID != "server1" {
		t.Errorf("Expected server ID to be %s, got %s", "server1", servers.Servers[0].ID)
	}
	if servers.Servers[0].Name != "test-server" {
		t.Errorf("Expected server name to be %s, got %s", "test-server", servers.Servers[0].Name)
	}
}

func TestGetHypervisors(t *testing.T) {
	// Mock the OpenStack Nova API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/os-hypervisors/detail" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			//nolint:errcheck
			json.NewEncoder(w).Encode(openStackHypervisorList{
				Hypervisors: []OpenStackHypervisor{
					{
						ID:       1,
						Hostname: "test-hypervisor",
					},
				},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	auth := openStackKeystoneAuth{
		nova: openStackEndpoint{
			URL: server.URL + "/",
		},
		token: "test-token",
	}

	// Call the function to test
	hypervisors, err := getHypervisors(auth, nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the results
	if len(hypervisors.Hypervisors) != 1 {
		t.Errorf("Expected 1 hypervisor, got %d", len(hypervisors.Hypervisors))
	}
	if hypervisors.Hypervisors[0].ID != 1 {
		t.Errorf("Expected hypervisor ID to be %d, got %d", 1, hypervisors.Hypervisors[0].ID)
	}
	if hypervisors.Hypervisors[0].Hostname != "test-hypervisor" {
		t.Errorf("Expected hypervisor hostname to be %s, got %s", "test-hypervisor", hypervisors.Hypervisors[0].Hostname)
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

	var hypervisor OpenStackHypervisor
	err := json.Unmarshal(data, &hypervisor)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if hypervisor.ID != 1 {
		t.Errorf("Expected ID to be %d, got %d", 1, hypervisor.ID)
	}
	if hypervisor.Hostname != "test-hypervisor" {
		t.Errorf("Expected hostname to be %s, got %s", "test-hypervisor", hypervisor.Hostname)
	}
	if hypervisor.ServiceID != 2 {
		t.Errorf("Expected ServiceID to be %d, got %d", 2, hypervisor.ServiceID)
	}
	if hypervisor.ServiceHost != "test-host" {
		t.Errorf("Expected ServiceHost to be %s, got %s", "test-host", hypervisor.ServiceHost)
	}
	if *hypervisor.ServiceDisabledReason != "maintenance" {
		t.Errorf("Expected ServiceDisabledReason to be %s, got %s", "maintenance", *hypervisor.ServiceDisabledReason)
	}
}

func TestMarshalOpenStackHypervisor(t *testing.T) {
	disabledReason := "maintenance"
	hypervisor := OpenStackHypervisor{
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
		t.Fatalf("Expected no error, got %v", err)
	}

	// Check if the data contains "service":
	if !json.Valid(data) {
		t.Error("Expected valid JSON, got invalid")
	}
	if !strings.Contains(string(data), "service") {
		t.Error("Expected JSON to contain 'service'")
	}
}

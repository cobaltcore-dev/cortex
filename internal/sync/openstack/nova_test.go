// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
)

func TestGetServers(t *testing.T) {
	// Mock the OpenStack Nova API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/servers/detail" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			//nolint:errcheck
			json.NewEncoder(w).Encode(ServerList{
				Servers: []Server{
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

	auth := KeystoneAuth{
		nova: Endpoint{
			URL: server.URL + "/",
		},
		token: "test-token",
	}

	api := NewObjectAPI[Server, ServerList](conf.SyncOpenStackConfig{}, sync.Monitor{})
	servers, err := api.List(auth)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the results
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if servers[0].ID != "server1" {
		t.Errorf("expected server ID to be %s, got %s", "server1", servers[0].ID)
	}
	if servers[0].Name != "test-server" {
		t.Errorf("expected server name to be %s, got %s", "test-server", servers[0].Name)
	}
}

func TestGetHypervisors(t *testing.T) {
	// Mock the OpenStack Nova API response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/os-hypervisors/detail" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			//nolint:errcheck
			json.NewEncoder(w).Encode(HypervisorList{
				Hypervisors: []Hypervisor{
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

	auth := KeystoneAuth{
		nova: Endpoint{
			URL: server.URL + "/",
		},
		token: "test-token",
	}

	api := NewObjectAPI[Hypervisor, HypervisorList](conf.SyncOpenStackConfig{}, sync.Monitor{})
	hypervisors, err := api.List(auth)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the results
	if len(hypervisors) != 1 {
		t.Errorf("expected 1 hypervisor, got %d", len(hypervisors))
	}
	if hypervisors[0].ID != 1 {
		t.Errorf("expected hypervisor ID to be %d, got %d", 1, hypervisors[0].ID)
	}
	if hypervisors[0].Hostname != "test-hypervisor" {
		t.Errorf("expected hypervisor hostname to be %s, got %s", "test-hypervisor", hypervisors[0].Hostname)
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

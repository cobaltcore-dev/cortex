// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		token: "test-token",
	}

	api := NewNovaAPI[Server, ServerList](conf.SyncOpenStackConfig{
		NovaURL: server.URL,
	}, sync.Monitor{})
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
		token: "test-token",
	}

	api := NewNovaAPI[Hypervisor, HypervisorList](conf.SyncOpenStackConfig{
		NovaURL: server.URL,
	}, sync.Monitor{})
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

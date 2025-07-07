// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
	"github.com/gophercloud/gophercloud/v2"
)

func setupManilaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	endpointLocator := func(gophercloud.EndpointOpts) (string, error) {
		return server.URL + "/", nil
	}
	return server, &testlibKeystone.MockKeystoneAPI{
		Url:             server.URL + "/",
		EndpointLocator: endpointLocator,
	}
}

func TestNewManilaAPI(t *testing.T) {
	mon := sync.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := ManilaConf{}

	api := NewManilaAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestManilaAPI_GetAllStoragePools(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"pools": []any{
				map[string]any{
					"name":    "test-pool",
					"host":    "host1",
					"backend": "backend1",
					"pool":    "pool1",
					"capabilities": map[string]any{
						"total_capacity_gb":                  100.0,
						"free_capacity_gb":                   50.0,
						"reserved_percentage":                10,
						"pool_name":                          "pool1",
						"share_backend_name":                 "backend1",
						"storage_protocol":                   "NFS",
						"vendor_name":                        "OpenStack",
						"replication_domain":                 nil,
						"sg_consistent_snapshot_support":     "True",
						"timestamp":                          "2024-06-12T12:00:00Z",
						"driver_version":                     "1.0.0",
						"replication_type":                   "dr",
						"driver_handles_share_servers":       true,
						"snapshot_support":                   true,
						"create_share_from_snapshot_support": true,
						"revert_to_snapshot_support":         false,
						"mount_snapshot_support":             false,
						"dedupe":                             false,
						"compression":                        []bool{true, false},
						"ipv4_support":                       nil,
						"ipv6_support":                       false,
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupManilaMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := ManilaConf{}

	api := NewManilaAPI(mon, k, conf).(*manilaAPI)
	api.Init(t.Context())

	ctx := t.Context()
	pools, err := api.GetAllStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 storage pool, got %d", len(pools))
	}
}

func TestManilaAPI_GetAllStoragePools_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(`{"error": "error fetching pools"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupManilaMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := ManilaConf{}

	api := NewManilaAPI(mon, k, conf).(*manilaAPI)
	api.Init(t.Context())

	ctx := t.Context()
	_, err := api.GetAllStoragePools(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
	"github.com/gophercloud/gophercloud/v2"
)

func setupCinderMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	endpointLocator := func(gophercloud.EndpointOpts) (string, error) {
		return server.URL + "/", nil
	}
	return server, &testlibKeystone.MockKeystoneAPI{
		Url:             server.URL + "/",
		EndpointLocator: endpointLocator,
	}
}

func TestNewCinderAPI(t *testing.T) {
	mon := sync.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := conf.SyncOpenStackCinderConfig{}

	api := NewCinderAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestCinderAPI_GetAllStoragePools(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"pools": []any{
				map[string]any{
					"name": "test-pool",
					"capabilities": map[string]any{
						"allocated_capacity_gb": 122,
						"backend_state":         "up",
						"custom_attributes": map[string]any{
							"cinder_state": "drain",
							"netapp_fqdn":  "test-netapp-fqdn",
						},
						"driver_version":             "driver-version",
						"free_capacity_gb":           1000,
						"multiattach":                false,
						"pool_down_reason":           "down reason",
						"pool_name":                  "pool-name",
						"pool_state":                 "down",
						"reserved_percentage":        20,
						"storage_protocol":           "storage-protocol",
						"thick_provisioning_support": true,
						"thin_provisioning_support":  false,
						"timestamp":                  "2025-08-18T11:45:00.000000",
						"total_capacity_gb":          5000,
						"vendor_name":                "VMware",
						"volume_backend_name":        "standard_hdd",
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupCinderMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := conf.SyncOpenStackCinderConfig{}

	api := NewCinderAPI(mon, k, conf).(*cinderAPI)
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

func TestCinderAPI_GetAllStoragePools_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(`{"error": "error fetching pools"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupCinderMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := conf.SyncOpenStackCinderConfig{}

	api := NewCinderAPI(mon, k, conf).(*cinderAPI)
	api.Init(t.Context())

	ctx := t.Context()
	_, err := api.GetAllStoragePools(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

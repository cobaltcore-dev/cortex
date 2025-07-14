// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

func setupKeystoneMockServer(handler http.HandlerFunc) (*httptest.Server, conf.KeystoneConfig) {
	server := httptest.NewServer(handler)
	conf := conf.KeystoneConfig{
		URL:                 server.URL + "/v3",
		OSUsername:          "testuser",
		OSUserDomainName:    "default",
		OSPassword:          "password",
		OSProjectName:       "testproject",
		OSProjectDomainName: "default",
	}
	return server, conf
}

func TestNewKeystoneAPI(t *testing.T) {
	keystoneConf := conf.KeystoneConfig{
		URL:                 "http://example.com",
		OSUsername:          "testuser",
		OSUserDomainName:    "default",
		OSPassword:          "password",
		OSProjectName:       "testproject",
		OSProjectDomainName: "default",
	}

	api := NewKeystoneAPI(keystoneConf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestKeystoneAPI_Authenticate(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte(`{"token": {"catalog": []}}`)); err != nil {
			t.Fatalf("error writing response: %v", err)
		}
	}
	server, keystoneConf := setupKeystoneMockServer(handler)
	defer server.Close()

	api := NewKeystoneAPI(keystoneConf).(*keystoneAPI)

	err := api.Authenticate(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if api.client == nil {
		t.Fatal("expected non-nil client after authentication")
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func setupKeystoneMockServer(handler http.HandlerFunc) (*httptest.Server, keystoneConfig) {
	server := httptest.NewServer(handler)
	conf := keystoneConfig{
		URL:                 server.URL + "/v3",
		OSUsername:          "testuser",
		OSUserDomainName:    "default",
		OSPassword:          "password",
		OSProjectName:       "testproject",
		OSProjectDomainName: "default",
	}
	return server, conf
}

func TestKeystoneClient_Authenticate(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte(`{"token": {"catalog": []}}`)); err != nil {
			t.Fatalf("error writing response: %v", err)
		}
	}
	server, keystoneConf := setupKeystoneMockServer(handler)
	defer server.Close()

	api := &keystoneClient{keystoneConf: keystoneConf}

	err := api.Authenticate(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if api.client == nil {
		t.Fatal("expected non-nil client after authentication")
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
)

func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneClient) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneClient{Url: server.URL + "/"}
}

func TestNewNovaAPI(t *testing.T) {
	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneClient{}
	conf := v1alpha1.NovaDatasource{}

	api := NewNovaAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestNovaAPI_GetDeletedServers(t *testing.T) {
	tests := []struct {
		Name string
		Time time.Time
	}{
		{
			Name: "should find default changes-since of 6 hours",
			Time: time.Now().Add(-6 * time.Hour),
		},
		{
			Name: "should find custom changes-since of 1 hour",
			Time: time.Now().Add(-1 * time.Hour),
		},
	}
	for _, tt := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("changes-since") != tt.Time.Format(time.RFC3339) {
				t.Fatalf("expected changes-since query parameter to be %s, got %s", tt.Time.Format(time.RFC3339), r.URL.Query().Get("changes-since"))
			}
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"servers": [{
				"id": "1",
				"name": "server1",
				"status": "DELETED",
				"flavor": {"id": "1", "name": "flavor1"}
			}]}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		}
		server, k := setupNovaMockServer(handler)
		defer server.Close()

		mon := datasources.Monitor{}
		conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers}

		api := NewNovaAPI(mon, k, conf).(*novaAPI)
		if err := api.Init(t.Context()); err != nil {
			t.Fatalf("failed to init cinder api: %v", err)
		}

		ctx := t.Context()
		servers, err := api.GetDeletedServers(ctx, tt.Time)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(servers))
		}
	}
}

func TestNovaAPI_GetAllServers(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"servers": [{
				"id": "1",
				"name": "server1",
				"flavor": {"id": "1", "name": "flavor1"}
			}]}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	servers, err := api.GetAllServers(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
}

func TestNovaAPI_GetAllServers_DeduplicatesServers(t *testing.T) {
	tests := []struct {
		name            string
		responses       []string
		expectedCount   int
		expectedServers []string
	}{
		{
			name: "duplicates within same page",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "server1", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "server2", "flavor": {"id": "1"}},
					{"id": "aaa", "name": "server1-dup", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount:   2,
			expectedServers: []string{"aaa", "bbb"},
		},
		{
			name: "duplicates across pages",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "server1", "flavor": {"id": "1"}}
				], "servers_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"servers": [
					{"id": "aaa", "name": "server1-dup", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "server2", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount:   2,
			expectedServers: []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates single page",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "server1", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "server2", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount:   2,
			expectedServers: []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates across pages",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "server1", "flavor": {"id": "1"}}
				], "servers_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"servers": [
					{"id": "bbb", "name": "server2", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount:   2,
			expectedServers: []string{"aaa", "bbb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte(tt.responses[callCount])); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				callCount++
			}
			srv, k := setupNovaMockServer(handler)
			defer srv.Close()

			// Patch NEXT_URL placeholder with actual server URL.
			for i := range tt.responses {
				tt.responses[i] = strings.ReplaceAll(tt.responses[i], "NEXT_URL", srv.URL+"/servers/detail?page=2")
			}

			api := NewNovaAPI(datasources.Monitor{}, k, v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers}).(*novaAPI)
			if err := api.Init(t.Context()); err != nil {
				t.Fatalf("failed to init nova api: %v", err)
			}

			servers, err := api.GetAllServers(t.Context())
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(servers) != tt.expectedCount {
				t.Fatalf("expected %d servers, got %d", tt.expectedCount, len(servers))
			}
			for i, id := range tt.expectedServers {
				if servers[i].ID != id {
					t.Fatalf("expected server[%d].ID = %s, got %s", i, id, servers[i].ID)
				}
			}
		})
	}
}

func TestNovaAPI_GetAllHypervisors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Hypervisors []Hypervisor `json:"hypervisors"`
		}{
			Hypervisors: []Hypervisor{{ID: "1", Hostname: "hypervisor1", CPUInfo: "{}"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeHypervisors}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	hypervisors, err := api.GetAllHypervisors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 1 {
		t.Fatalf("expected 1 hypervisor, got %d", len(hypervisors))
	}
}

func TestNovaAPI_GetAllFlavors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// We only want the current state of all flavors, so
		// the changes-since query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Flavors []Flavor `json:"flavors"`
		}{
			Flavors: []Flavor{{ID: "1", Name: "flavor1"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeFlavors}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	flavors, err := api.GetAllFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(flavors) != 1 {
		t.Fatalf("expected 1 flavor, got %d", len(flavors))
	}
}

func TestNovaAPI_GetAllMigrations(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Migrations []Migration `json:"migrations"`
			Links      []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"migrations_links"`
		}{
			Migrations: []Migration{{ID: 1, SourceCompute: "host1", DestCompute: "host2", Status: "completed"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}

	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeMigrations}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	migrations, err := api.GetAllMigrations(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}
	if migrations[0].ID != 1 || migrations[0].SourceCompute != "host1" || migrations[0].DestCompute != "host2" || migrations[0].Status != "completed" {
		t.Errorf("unexpected migration data: %+v", migrations[0])
	}
}

func TestNovaAPI_GetDeletedServers_DeduplicatesServers(t *testing.T) {
	tests := []struct {
		name          string
		responses     []string
		expectedCount int
		expectedIDs   []string
	}{
		{
			name: "duplicates within same page",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "s1", "status": "DELETED", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "s2", "status": "DELETED", "flavor": {"id": "1"}},
					{"id": "aaa", "name": "s1-dup", "status": "DELETED", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "duplicates across pages",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "s1", "status": "DELETED", "flavor": {"id": "1"}}
				], "servers_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"servers": [
					{"id": "aaa", "name": "s1-dup", "status": "DELETED", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "s2", "status": "DELETED", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates single page",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "s1", "status": "DELETED", "flavor": {"id": "1"}},
					{"id": "bbb", "name": "s2", "status": "DELETED", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates across pages",
			responses: []string{
				`{"servers": [
					{"id": "aaa", "name": "s1", "status": "DELETED", "flavor": {"id": "1"}}
				], "servers_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"servers": [
					{"id": "bbb", "name": "s2", "status": "DELETED", "flavor": {"id": "1"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte(tt.responses[callCount])); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				callCount++
			}
			srv, k := setupNovaMockServer(handler)
			defer srv.Close()

			for i := range tt.responses {
				tt.responses[i] = strings.ReplaceAll(tt.responses[i], "NEXT_URL", srv.URL+"/servers/detail?page=2")
			}

			api := NewNovaAPI(datasources.Monitor{}, k, v1alpha1.NovaDatasource{}).(*novaAPI)
			if err := api.Init(t.Context()); err != nil {
				t.Fatalf("failed to init nova api: %v", err)
			}

			servers, err := api.GetDeletedServers(t.Context(), time.Now().Add(-6*time.Hour))
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(servers) != tt.expectedCount {
				t.Fatalf("expected %d servers, got %d", tt.expectedCount, len(servers))
			}
			for i, id := range tt.expectedIDs {
				if servers[i].ID != id {
					t.Fatalf("expected server[%d].ID = %s, got %s", i, id, servers[i].ID)
				}
			}
		})
	}
}

func TestNovaAPI_GetAllHypervisors_DeduplicatesHypervisors(t *testing.T) {
	tests := []struct {
		name          string
		responses     []string
		expectedCount int
		expectedIDs   []string
	}{
		{
			name: "duplicates within same page",
			responses: []string{
				`{"hypervisors": [
					{"id": "aaa", "hypervisor_hostname": "h1", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}},
					{"id": "bbb", "hypervisor_hostname": "h2", "cpu_info": {}, "service": {"id": "s2", "host": "h2"}},
					{"id": "aaa", "hypervisor_hostname": "h1-dup", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "duplicates across pages",
			responses: []string{
				`{"hypervisors": [
					{"id": "aaa", "hypervisor_hostname": "h1", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}}
				], "hypervisors_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"hypervisors": [
					{"id": "aaa", "hypervisor_hostname": "h1-dup", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}},
					{"id": "bbb", "hypervisor_hostname": "h2", "cpu_info": {}, "service": {"id": "s2", "host": "h2"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates single page",
			responses: []string{
				`{"hypervisors": [
					{"id": "aaa", "hypervisor_hostname": "h1", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}},
					{"id": "bbb", "hypervisor_hostname": "h2", "cpu_info": {}, "service": {"id": "s2", "host": "h2"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates across pages",
			responses: []string{
				`{"hypervisors": [
					{"id": "aaa", "hypervisor_hostname": "h1", "cpu_info": {}, "service": {"id": "s1", "host": "h1"}}
				], "hypervisors_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"hypervisors": [
					{"id": "bbb", "hypervisor_hostname": "h2", "cpu_info": {}, "service": {"id": "s2", "host": "h2"}}
				]}`,
			},
			expectedCount: 2,
			expectedIDs:   []string{"aaa", "bbb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte(tt.responses[callCount])); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				callCount++
			}
			srv, k := setupNovaMockServer(handler)
			defer srv.Close()

			for i := range tt.responses {
				tt.responses[i] = strings.ReplaceAll(tt.responses[i], "NEXT_URL", srv.URL+"/os-hypervisors/detail?page=2")
			}

			api := NewNovaAPI(datasources.Monitor{}, k, v1alpha1.NovaDatasource{}).(*novaAPI)
			if err := api.Init(t.Context()); err != nil {
				t.Fatalf("failed to init nova api: %v", err)
			}

			hypervisors, err := api.GetAllHypervisors(t.Context())
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(hypervisors) != tt.expectedCount {
				t.Fatalf("expected %d hypervisors, got %d", tt.expectedCount, len(hypervisors))
			}
			for i, id := range tt.expectedIDs {
				if hypervisors[i].ID != id {
					t.Fatalf("expected hypervisor[%d].ID = %s, got %s", i, id, hypervisors[i].ID)
				}
			}
		})
	}
}

func TestNovaAPI_GetAllMigrations_DeduplicatesMigrations(t *testing.T) {
	tests := []struct {
		name          string
		responses     []string
		expectedCount int
		expectedUUIDs []string
	}{
		{
			name: "duplicates within same page",
			responses: []string{
				`{"migrations": [
					{"id": 1, "uuid": "aaa", "status": "completed"},
					{"id": 2, "uuid": "bbb", "status": "completed"},
					{"id": 1, "uuid": "aaa", "status": "completed"}
				]}`,
			},
			expectedCount: 2,
			expectedUUIDs: []string{"aaa", "bbb"},
		},
		{
			name: "duplicates across pages",
			responses: []string{
				`{"migrations": [
					{"id": 1, "uuid": "aaa", "status": "completed"}
				], "migrations_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"migrations": [
					{"id": 1, "uuid": "aaa", "status": "completed"},
					{"id": 2, "uuid": "bbb", "status": "completed"}
				]}`,
			},
			expectedCount: 2,
			expectedUUIDs: []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates single page",
			responses: []string{
				`{"migrations": [
					{"id": 1, "uuid": "aaa", "status": "completed"},
					{"id": 2, "uuid": "bbb", "status": "completed"}
				]}`,
			},
			expectedCount: 2,
			expectedUUIDs: []string{"aaa", "bbb"},
		},
		{
			name: "no duplicates across pages",
			responses: []string{
				`{"migrations": [
					{"id": 1, "uuid": "aaa", "status": "completed"}
				], "migrations_links": [{"rel": "next", "href": "NEXT_URL"}]}`,
				`{"migrations": [
					{"id": 2, "uuid": "bbb", "status": "completed"}
				]}`,
			},
			expectedCount: 2,
			expectedUUIDs: []string{"aaa", "bbb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte(tt.responses[callCount])); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				callCount++
			}
			srv, k := setupNovaMockServer(handler)
			defer srv.Close()

			for i := range tt.responses {
				tt.responses[i] = strings.ReplaceAll(tt.responses[i], "NEXT_URL", srv.URL+"/os-migrations?page=2")
			}

			api := NewNovaAPI(datasources.Monitor{}, k, v1alpha1.NovaDatasource{}).(*novaAPI)
			if err := api.Init(t.Context()); err != nil {
				t.Fatalf("failed to init nova api: %v", err)
			}

			migrations, err := api.GetAllMigrations(t.Context())
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(migrations) != tt.expectedCount {
				t.Fatalf("expected %d migrations, got %d", tt.expectedCount, len(migrations))
			}
			for i, uuid := range tt.expectedUUIDs {
				if migrations[i].UUID != uuid {
					t.Fatalf("expected migration[%d].UUID = %s, got %s", i, uuid, migrations[i].UUID)
				}
			}
		})
	}
}

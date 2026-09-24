// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/gophercloud/gophercloud/v2"
	"k8s.io/apimachinery/pkg/api/resource"
)

// setupNovaMockServer starts an httptest server backed by handler and returns
// a mock keystone client whose provider client can drive the gophercloud
// service client at that server's URL.
func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneClient) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneClient{Url: server.URL + "/"}
}

// newTestNovaVMClient wires a novaVMClient against the given test server and
// keystone mock.
func newTestNovaVMClient(server *httptest.Server, k keystone.KeystoneClient) *novaVMClient {
	return &novaVMClient{
		sc: &gophercloud.ServiceClient{
			ProviderClient: k.Client(),
			Endpoint:       server.URL + "/",
			Type:           "compute",
			Microversion:   "2.53",
		},
	}
}

func TestNovaVMClient_GetCurrentVMSize(t *testing.T) {
	const vmID = "vm-abc"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET method, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/servers/"+vmID) {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"server": {"id": "vm-abc", "flavor": {"ram": 2048, "vcpus": 4}}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	c := newTestNovaVMClient(server, k)

	size, err := c.GetCurrentVMSize(t.Context(), vmID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// CPU is DecimalSI-encoded quantity of vcpus.
	cpu, ok := size[hv1.ResourceCPU]
	if !ok {
		t.Fatalf("expected CPU resource in size map: %+v", size)
	}
	if got := cpu.Value(); got != 4 {
		t.Errorf("expected 4 vCPUs, got %d", got)
	}
	// Memory is reported in bytes: ram (MiB) * 1024 * 1024.
	mem, ok := size[hv1.ResourceMemory]
	if !ok {
		t.Fatalf("expected Memory resource in size map: %+v", size)
	}
	wantMem := int64(2048) * 1024 * 1024
	if got := mem.Value(); got != wantMem {
		t.Errorf("expected %d bytes memory, got %d", wantMem, got)
	}
	if mem.Format != resource.BinarySI {
		t.Errorf("expected BinarySI format for memory, got %v", mem.Format)
	}
	if cpu.Format != resource.DecimalSI {
		t.Errorf("expected DecimalSI format for cpu, got %v", cpu.Format)
	}
}

func TestNovaVMClient_GetCurrentVMSize_ZeroValues(t *testing.T) {
	// Server that returns a flavor with zero ram/vcpus — we still expect
	// a valid, non-nil size map (values are zero) and no error.
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"server": {"id": "vm-zero", "flavor": {"ram": 0, "vcpus": 0}}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	c := newTestNovaVMClient(server, k)

	size, err := c.GetCurrentVMSize(t.Context(), "vm-zero")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	cpu := size[hv1.ResourceCPU]
	if got := cpu.Value(); got != 0 {
		t.Errorf("expected 0 vCPUs, got %d", got)
	}
	mem := size[hv1.ResourceMemory]
	if got := mem.Value(); got != 0 {
		t.Errorf("expected 0 bytes memory, got %d", got)
	}
}

func TestNovaVMClient_GetCurrentVMSize_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte(`{"itemNotFound": {"message": "Instance not found", "code": 404}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	c := newTestNovaVMClient(server, k)

	_, err := c.GetCurrentVMSize(t.Context(), "missing-vm")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestNovaVMClient_GetCurrentVMSize_ServerError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	c := newTestNovaVMClient(server, k)

	_, err := c.GetCurrentVMSize(t.Context(), "any-vm")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestNovaVMClient_GetCurrentVMSize_MalformedJSON(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`not a json`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	c := newTestNovaVMClient(server, k)

	_, err := c.GetCurrentVMSize(t.Context(), "any-vm")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

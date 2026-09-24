// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"net/http/httptest"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// Test helper factories
// ---------------------------------------------------------------------------

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("hv1 scheme: %v", err)
	}
	return s
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := testScheme(t)
	builder := fake.NewClientBuilder().WithScheme(s)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	builder = builder.WithIndex(&hv1.Hypervisor{}, idxHypervisorOpenStackId, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			return nil
		}
		if hv.Status.HypervisorID == "" {
			return nil
		}
		return []string{hv.Status.HypervisorID}
	})
	builder = builder.WithIndex(&hv1.Hypervisor{}, idxHypervisorName, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			return nil
		}
		return []string{hv.Name}
	})
	return builder.Build()
}

// newTestShimWithHypervisors builds a shim wired to a live upstream test server
// and a fake Kubernetes client seeded with the given hypervisors. The feature
// toggles default to off (pure passthrough); individual tests flip the relevant
// toggle when they need the 501 path.
func newTestShimWithHypervisors(t *testing.T, upstreamStatus int, upstreamBody string, hvs ...client.Object) *Shim {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		if _, err := w.Write([]byte(upstreamBody)); err != nil {
			t.Errorf("failed to write upstream body: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	down, up := newTestTimers()
	return &Shim{
		Client: newFakeClient(t, hvs...),
		config: config{
			PlacementURL: upstream.URL,
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
}

// ---------------------------------------------------------------------------
// Passthrough handler tests (toggle off)
// ---------------------------------------------------------------------------

func TestHandleListResourceProviders(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"resource_providers":[]}`, &gotPath)
	w := serveHandler(t, http.MethodGet, "/resource_providers",
		s.HandleListResourceProviders, "/resource_providers")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_providers" {
		t.Fatalf("upstream path = %q, want /resource_providers", gotPath)
	}
}

func TestHandleCreateResourceProvider(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, `{}`, nil)
	req := httptest.NewRequest(http.MethodPost, "/resource_providers", http.NoBody)
	w := httptest.NewRecorder()
	s.HandleCreateResourceProvider(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleShowResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, `{}`, nil)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, `{}`, nil)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestHandleUpdateResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, `{}`, nil)
		w := serveHandler(t, http.MethodPut, "/resource_providers/{uuid}",
			s.HandleUpdateResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, `{}`, nil)
		w := serveHandler(t, http.MethodPut, "/resource_providers/{uuid}",
			s.HandleUpdateResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestHandleDeleteResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
}

// ---------------------------------------------------------------------------
// Enabled toggle tests (KVM mapping not yet implemented -> 501)
// ---------------------------------------------------------------------------

func TestHandleResourceProviders_Enabled(t *testing.T) {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-01"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}
	s := newTestShimWithHypervisors(t, http.StatusOK, `{}`, hv)
	s.config.Features.ResourceProviders = true

	t.Run("list returns 501", func(t *testing.T) {
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("create returns 501", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", http.NoBody)
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("show returns 501", func(t *testing.T) {
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("update returns 501", func(t *testing.T) {
		w := serveHandler(t, http.MethodPut, "/resource_providers/{uuid}",
			s.HandleUpdateResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("delete returns 501", func(t *testing.T) {
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("invalid uuid returns 400", func(t *testing.T) {
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

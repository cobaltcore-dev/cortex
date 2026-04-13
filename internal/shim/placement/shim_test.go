// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validUUID = "d9b3a520-2a3c-4f6b-8b9a-1c2d3e4f5a6b"

// newTestShim creates a Shim backed by an upstream test server that returns
// the given status and body for every request. It records the last request
// path in *gotPath when non-nil.
func newTestShim(t *testing.T, status int, body string, gotPath *string) *Shim {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			*gotPath = r.URL.Path
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("failed to write response body: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	return &Shim{
		config:     config{PlacementURL: upstream.URL},
		httpClient: upstream.Client(),
	}
}

// serveHandler registers a single handler on a fresh mux and serves the
// request through it, returning the recorded response.
func serveHandler(t *testing.T, method, pattern string, handler http.HandlerFunc, reqPath string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(method+" "+pattern, handler)
	req := httptest.NewRequest(method, reqPath, http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestForward(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		query          string
		method         string
		body           string
		reqHeaders     map[string]string
		upstreamStatus int
		upstreamBody   string
		upstreamHeader map[string]string
	}{
		{
			name:           "GET with query string",
			path:           "/resource_providers",
			query:          "name=test",
			method:         "GET",
			upstreamStatus: http.StatusOK,
			upstreamBody:   `{"resource_providers":[]}`,
			upstreamHeader: map[string]string{"Content-Type": "application/json"},
		},
		{
			name:           "PUT with body and headers",
			path:           "/resource_providers/abc",
			method:         "PUT",
			body:           `{"name":"new"}`,
			reqHeaders:     map[string]string{"X-Custom": "val"},
			upstreamStatus: http.StatusOK,
			upstreamBody:   `{"uuid":"abc"}`,
		},
		{
			name:           "upstream error",
			path:           "/fail",
			method:         "GET",
			upstreamStatus: http.StatusNotFound,
			upstreamBody:   "not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the path and query were forwarded.
				if r.URL.Path != tt.path {
					t.Errorf("upstream path = %q, want %q", r.URL.Path, tt.path)
				}
				if r.URL.RawQuery != tt.query {
					t.Errorf("upstream query = %q, want %q", r.URL.RawQuery, tt.query)
				}
				if r.Method != tt.method {
					t.Errorf("upstream method = %q, want %q", r.Method, tt.method)
				}
				// Verify headers were copied.
				for k, v := range tt.reqHeaders {
					if got := r.Header.Get(k); got != v {
						t.Errorf("upstream header %q = %q, want %q", k, got, v)
					}
				}
				// Verify body was copied.
				if tt.body != "" {
					b, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatalf("failed to read upstream body: %v", err)
					}
					if string(b) != tt.body {
						t.Errorf("upstream body = %q, want %q", string(b), tt.body)
					}
				}
				for k, v := range tt.upstreamHeader {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.upstreamStatus)
				if _, err := w.Write([]byte(tt.upstreamBody)); err != nil {
					t.Fatalf("failed to write upstream body: %v", err)
				}
			}))
			defer upstream.Close()

			s := &Shim{
				config:     config{PlacementURL: upstream.URL},
				httpClient: upstream.Client(),
			}
			target := tt.path
			if tt.query != "" {
				target += "?" + tt.query
			}
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, target, bodyReader)
			for k, v := range tt.reqHeaders {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			s.forward(w, req)

			if w.Code != tt.upstreamStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.upstreamStatus)
			}
			if got := w.Body.String(); got != tt.upstreamBody {
				t.Fatalf("body = %q, want %q", got, tt.upstreamBody)
			}
			for k, v := range tt.upstreamHeader {
				if got := w.Header().Get(k); got != v {
					t.Errorf("response header %q = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestForwardUpstreamUnreachable(t *testing.T) {
	s := &Shim{
		config:     config{PlacementURL: "http://127.0.0.1:1"},
		httpClient: &http.Client{},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	s.forward(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestRegisterRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	s := &Shim{
		config:     config{PlacementURL: upstream.URL},
		httpClient: upstream.Client(),
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	// Verify a sample of routes are registered. Unregistered patterns
	// return 404 from the default mux; registered ones reach the upstream.
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/resource_providers"},
		{"POST", "/resource_providers"},
		{"GET", "/traits"},
		{"GET", "/allocation_candidates"},
		{"POST", "/reshaper"},
		{"POST", "/allocations"},
		{"GET", "/usages"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, http.NoBody)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Fatalf("route %s %s returned 404, expected it to be registered", rt.method, rt.path)
			}
		})
	}
}

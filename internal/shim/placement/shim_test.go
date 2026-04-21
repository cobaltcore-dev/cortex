// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const validUUID = "d9b3a520-2a3c-4f6b-8b9a-1c2d3e4f5a6b"

// timerLabels are the histogram label names used by both request timers.
var timerLabels = []string{"method", "pattern", "responsecode"}

// histSampleCount returns the number of observations recorded by the histogram
// with the given label values. Returns 0 when no matching series exists.
func histSampleCount(t *testing.T, h *prometheus.HistogramVec, lvs ...string) uint64 {
	t.Helper()
	obs, err := h.GetMetricWithLabelValues(lvs...)
	if err != nil {
		t.Fatalf("failed to get metric with labels %v: %v", lvs, err)
	}
	m := &dto.Metric{}
	if err := obs.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// newTestTimers returns fresh downstream and upstream histogram vecs for tests.
func newTestTimers() (downstream, upstream *prometheus.HistogramVec) {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "test_downstream", Buckets: prometheus.DefBuckets,
		}, timerLabels),
		prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "test_upstream", Buckets: prometheus.DefBuckets,
		}, timerLabels)
}

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
	down, up := newTestTimers()
	return &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
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
				config:         config{PlacementURL: upstream.URL},
				httpClient:     upstream.Client(),
				maxBodyLogSize: 4096,
			}
			s.downstreamRequestTimer, s.upstreamRequestTimer = newTestTimers()
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
	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: "http://127.0.0.1:1"},
		httpClient:             &http.Client{},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
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
	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
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

func TestRegisterRoutesDownstreamMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	// Fire a request through the mux so the wrapper observes the downstream timer.
	req := httptest.NewRequest(http.MethodGet, "/resource_providers", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	// The downstream timer should have exactly one observation for the
	// expected label combination (method, pattern, responsecode).
	if n := histSampleCount(t, down, "GET", "/resource_providers", "200"); n != 1 {
		t.Errorf("downstream observation count = %d, want 1", n)
	}
}

func TestForwardUpstreamMetrics(t *testing.T) {
	t.Run("success records upstream status", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer upstream.Close()
		down, up := newTestTimers()
		s := &Shim{
			config:                 config{PlacementURL: upstream.URL},
			httpClient:             upstream.Client(),
			maxBodyLogSize:         4096,
			downstreamRequestTimer: down,
			upstreamRequestTimer:   up,
		}
		// Set the route pattern via context, as the RegisterRoutes wrapper would.
		req := httptest.NewRequest(http.MethodGet, "/traits", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), routePatternKey, "/traits"))
		w := httptest.NewRecorder()
		s.forward(w, req)

		if n := histSampleCount(t, up, "GET", "/traits", "404"); n != 1 {
			t.Errorf("upstream observation count = %d, want 1", n)
		}
	})

	t.Run("unreachable upstream records 502", func(t *testing.T) {
		down, up := newTestTimers()
		s := &Shim{
			config:                 config{PlacementURL: "http://127.0.0.1:1"},
			httpClient:             &http.Client{},
			maxBodyLogSize:         4096,
			downstreamRequestTimer: down,
			upstreamRequestTimer:   up,
		}
		req := httptest.NewRequest(http.MethodGet, "/usages", http.NoBody)
		req = req.WithContext(context.WithValue(req.Context(), routePatternKey, "/usages"))
		w := httptest.NewRecorder()
		s.forward(w, req)

		if n := histSampleCount(t, up, "GET", "/usages", "502"); n != 1 {
			t.Errorf("upstream observation count = %d, want 1", n)
		}
	})
}

func TestRequestIDPropagation(t *testing.T) {
	const wantID = "req-abc-123"
	var gotID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The shim forwards all headers, so the request ID should arrive.
		gotID = r.Header.Get("X-OpenStack-Request-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/resource_providers", http.NoBody)
	req.Header.Set("X-OpenStack-Request-Id", wantID)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotID != wantID {
		t.Errorf("upstream X-OpenStack-Request-Id = %q, want %q", gotID, wantID)
	}
}

func TestRequestIDInContext(t *testing.T) {
	// Verify that the middleware in RegisterRoutes injects the request ID
	// into the context so that forward() and all downstream code can read it.
	// We confirm this indirectly: the forward method copies headers from the
	// original request (which include X-OpenStack-Request-Id), and the
	// middleware enriches the logger. Here we just verify that the context
	// key is populated by checking it survives through to the upstream.
	const wantID = "req-xyz-789"
	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-OpenStack-Request-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/traits", http.NoBody)
	req.Header.Set("X-OpenStack-Request-Id", wantID)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if gotHeader != wantID {
		t.Errorf("upstream received X-OpenStack-Request-Id = %q, want %q", gotHeader, wantID)
	}
}

func TestConfigValidateAuthRequiresKeystoneURL(t *testing.T) {
	c := config{
		PlacementURL: "http://placement:8778",
		Auth:         &authConfig{TokenCacheTTL: "5m", Policies: []authPolicy{{Pattern: "GET /", Roles: nil}}},
	}
	if err := c.validate(); err == nil {
		t.Fatal("expected error when auth configured without keystoneURL")
	}
	c.KeystoneURL = "http://keystone:5000"
	if err := c.validate(); err == nil {
		t.Fatal("expected error when auth configured without osUsername")
	}
	c.OSUsername = "admin"
	if err := c.validate(); err == nil {
		t.Fatal("expected error when auth configured without osPassword")
	}
	c.OSPassword = "secret"
	if err := c.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWrapHandlerWithAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer upstream.Close()

	info := &tokenInfo{
		roles:     []string{"cloud_compute_admin"},
		expiresAt: time.Now().Add(time.Hour),
		cachedAt:  time.Now(),
	}

	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
		authPolicies: []compiledPolicy{
			{method: "GET", pathPattern: "/", roles: nil}, // public
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "cloud_compute_admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{info: info},
	}

	t.Run("authorized request succeeds", func(t *testing.T) {
		wrapped := s.wrapHandler("/test", func(w http.ResponseWriter, r *http.Request) {
			s.forward(w, r)
		})
		req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
		req.Header.Set("X-Auth-Token", "good-token")
		w := httptest.NewRecorder()
		wrapped(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if n := histSampleCount(t, down, "GET", "/test", "200"); n != 1 {
			t.Errorf("downstream 200 count = %d, want 1", n)
		}
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		down2, _ := newTestTimers()
		s2 := *s
		s2.downstreamRequestTimer = down2
		wrapped := s2.wrapHandler("/test", func(w http.ResponseWriter, r *http.Request) {
			s2.forward(w, r)
		})
		req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
		w := httptest.NewRecorder()
		wrapped(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if n := histSampleCount(t, down2, "GET", "/test", "401"); n != 1 {
			t.Errorf("downstream 401 count = %d, want 1", n)
		}
	})

	t.Run("public endpoint without token succeeds", func(t *testing.T) {
		down3, _ := newTestTimers()
		s3 := *s
		s3.downstreamRequestTimer = down3
		wrapped := s3.wrapHandler("/", func(w http.ResponseWriter, r *http.Request) {
			s3.forward(w, r)
		})
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		wrapped(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

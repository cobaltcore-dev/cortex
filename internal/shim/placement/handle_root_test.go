// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetRootPassthrough(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"versions":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/")
	}
}

func TestHandleGetRootStatic(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Root: FeatureModeCRD},
			Versioning: &versioningConfig{
				ID:         "v1.0",
				MinVersion: "1.0",
				MaxVersion: "1.39",
				Status:     "CURRENT",
			},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}

	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}

	var doc versionDocument
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(doc.Versions) != 1 {
		t.Fatalf("versions count = %d, want 1", len(doc.Versions))
	}
	v := doc.Versions[0]
	if v.ID != "v1.0" {
		t.Errorf("id = %q, want %q", v.ID, "v1.0")
	}
	if v.MinVersion != "1.0" {
		t.Errorf("min_version = %q, want %q", v.MinVersion, "1.0")
	}
	if v.MaxVersion != "1.39" {
		t.Errorf("max_version = %q, want %q", v.MaxVersion, "1.39")
	}
	if v.Status != "CURRENT" {
		t.Errorf("status = %q, want %q", v.Status, "CURRENT")
	}
	if len(v.Links) != 1 || v.Links[0].Rel != "self" || v.Links[0].Href != "" {
		t.Errorf("links = %+v, want [{Rel:self Href:}]", v.Links)
	}
}

func TestHandleGetRootStaticDoesNotCallUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called when root mode is crd")
	}))
	t.Cleanup(upstream.Close)
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: upstream.URL,
			Features:     featuresConfig{Root: FeatureModeCRD},
			Versioning: &versioningConfig{
				ID:         "v1.0",
				MinVersion: "1.0",
				MaxVersion: "1.39",
				Status:     "CURRENT",
			},
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}

	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetRootHybrid(t *testing.T) {
	// Upstream returns a version document with maxVersion "1.35".
	upstreamDoc := versionDocument{
		Versions: []versionEntry{{
			ID:         "v1.0",
			MaxVersion: "1.35",
			MinVersion: "1.0",
			Status:     "CURRENT",
			Links:      []versionLink{{Rel: "self", Href: ""}},
		}},
	}
	upstreamBody, err := json.Marshal(upstreamDoc)
	if err != nil {
		t.Fatalf("failed to marshal upstream doc: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(upstreamBody); err != nil {
			t.Errorf("failed to write upstream body: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)

	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: upstream.URL,
			Features:     featuresConfig{Root: FeatureModeHybrid},
			Versioning: &versioningConfig{
				ID:         "v1.0",
				MinVersion: "1.0",
				MaxVersion: "1.39",
				Status:     "CURRENT",
			},
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}

	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}

	var doc versionDocument
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(doc.Versions) != 1 {
		t.Fatalf("versions count = %d, want 1", len(doc.Versions))
	}
	v := doc.Versions[0]
	// The intersection of upstream [1.0, 1.35] and local [1.0, 1.39]
	// should yield min "1.0" and max "1.35" (the narrower range).
	if v.MinVersion != "1.0" {
		t.Errorf("min_version = %q, want %q", v.MinVersion, "1.0")
	}
	if v.MaxVersion != "1.35" {
		t.Errorf("max_version = %q, want %q", v.MaxVersion, "1.35")
	}
	if v.ID != "v1.0" {
		t.Errorf("id = %q, want %q", v.ID, "v1.0")
	}
	if v.Status != "CURRENT" {
		t.Errorf("status = %q, want %q", v.Status, "CURRENT")
	}
}

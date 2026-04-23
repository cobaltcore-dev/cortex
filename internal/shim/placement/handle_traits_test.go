// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/resourcelock"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClientWithScheme(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := coordinationv1.AddToScheme(s); err != nil {
		t.Fatalf("add coordination scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func newTestConfigMap(namespace, name string, traits []string) *corev1.ConfigMap {
	b, err := json.Marshal(traits)
	if err != nil {
		panic("marshal traits: " + err.Error())
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string]string{configMapKeyTraits: string(b)},
	}
}

func newTraitShim(t *testing.T, staticTraits []string, customTraits ...string) *Shim {
	t.Helper()
	t.Setenv("POD_NAMESPACE", "default")
	objs := []client.Object{newTestConfigMap("default", "test-cm", staticTraits)}
	if len(customTraits) > 0 {
		objs = append(objs, newTestConfigMap("default", "test-cm-custom", customTraits))
	}
	cl := newFakeClientWithScheme(t, objs...)
	down, up := newTestTimers()
	return &Shim{
		Client: cl,
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{EnableTraits: true},
			Traits:       &traitsConfig{ConfigMapName: "test-cm"},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
		resourceLocker:         resourcelock.NewResourceLocker(cl, "default"),
	}
}

// --- Passthrough tests (enableTraits=false) ---

func TestHandleListTraitsPassthrough(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"traits":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/traits" {
		t.Fatalf("upstream path = %q, want /traits", gotPath)
	}
}

func TestHandleShowTraitPassthrough(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusNoContent, "", &gotPath)
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/HW_CPU_X86_AVX2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if gotPath != "/traits/HW_CPU_X86_AVX2" {
		t.Fatalf("upstream path = %q, want /traits/HW_CPU_X86_AVX2", gotPath)
	}
}

func TestHandleUpdateTraitPassthrough(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, "", nil)
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_TRAIT")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleDeleteTraitPassthrough(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/CUSTOM_TRAIT")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// --- Handler tests (enableTraits=true) ---

func TestHandleListTraitsLocal(t *testing.T) {
	s := newTraitShim(t, []string{"CUSTOM_FOO", "HW_CPU_X86_AVX2", "STORAGE_DISK_SSD"})

	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp traitsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Traits) != 3 {
		t.Fatalf("got %d traits, want 3: %v", len(resp.Traits), resp.Traits)
	}
	want := []string{"CUSTOM_FOO", "HW_CPU_X86_AVX2", "STORAGE_DISK_SSD"}
	for i, g := range resp.Traits {
		if g != want[i] {
			t.Errorf("trait[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestHandleListTraitsLocalMerged(t *testing.T) {
	s := newTraitShim(t, []string{"HW_CPU_X86_AVX2"}, "CUSTOM_FOO")

	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp traitsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"CUSTOM_FOO", "HW_CPU_X86_AVX2"}
	if len(resp.Traits) != len(want) {
		t.Fatalf("got %d traits, want %d: %v", len(resp.Traits), len(want), resp.Traits)
	}
	for i, g := range resp.Traits {
		if g != want[i] {
			t.Errorf("trait[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestHandleListTraitsLocalFiltered(t *testing.T) {
	s := newTraitShim(t, []string{"CUSTOM_A", "CUSTOM_B", "HW_CPU_X86_AVX2"})

	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits?name=in:CUSTOM_A,HW_CPU_X86_AVX2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp traitsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Traits) != 2 {
		t.Fatalf("got %d traits, want 2: %v", len(resp.Traits), resp.Traits)
	}
}

func TestHandleListTraitsLocalStartsWith(t *testing.T) {
	s := newTraitShim(t, []string{"CUSTOM_A", "CUSTOM_B", "HW_CPU_X86_AVX2"})

	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits?name=startswith:CUSTOM_")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp traitsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Traits) != 2 {
		t.Fatalf("got %d traits, want 2: %v", len(resp.Traits), resp.Traits)
	}
}

func TestHandleListTraitsLocalFilterUnknown(t *testing.T) {
	s := newTraitShim(t, []string{"CUSTOM_A"})

	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits?name=in:UNKNOWN_TRAIT")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp traitsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Traits) != 0 {
		t.Fatalf("got %v, want empty", resp.Traits)
	}
}

func TestHandleShowTraitLocalFound(t *testing.T) {
	s := newTraitShim(t, []string{"HW_CPU_X86_AVX2"})
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/HW_CPU_X86_AVX2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleShowTraitLocalFoundCustom(t *testing.T) {
	s := newTraitShim(t, nil, "CUSTOM_FOO")
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/CUSTOM_FOO")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleShowTraitLocalNotFound(t *testing.T) {
	s := newTraitShim(t, []string{"HW_CPU_X86_AVX2"})
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/NONEXISTENT")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateTraitLocalCreated(t *testing.T) {
	s := newTraitShim(t, nil)
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_NEW")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	found, err := s.hasTrait(context.Background(), "CUSTOM_NEW")
	if err != nil {
		t.Fatalf("hasTrait: %v", err)
	}
	if !found {
		t.Error("expected trait to be in store")
	}
}

func TestHandleUpdateTraitLocalAlreadyExistsCustom(t *testing.T) {
	s := newTraitShim(t, nil, "CUSTOM_EXISTING")
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_EXISTING")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleUpdateTraitLocalAlreadyExistsStatic(t *testing.T) {
	s := newTraitShim(t, []string{"HW_CPU_X86_AVX2"})
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/HW_CPU_X86_AVX2")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdateTraitLocalBadPrefix(t *testing.T) {
	s := newTraitShim(t, nil)
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/HW_NOT_CUSTOM")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdateTraitLocalSyncsToUpstream(t *testing.T) {
	var gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	s := newTraitShim(t, nil)
	s.config.PlacementURL = upstream.URL
	s.httpClient = upstream.Client()

	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_NEW")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if gotMethod != "PUT" || gotPath != "/traits/CUSTOM_NEW" {
		t.Fatalf("upstream got %s %s, want PUT /traits/CUSTOM_NEW", gotMethod, gotPath)
	}
}

func TestHandleUpdateTraitLocalUpstreamDown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	s := newTraitShim(t, nil)
	s.config.PlacementURL = upstream.URL
	s.httpClient = upstream.Client()

	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_NEW")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; upstream failure should not block local creation", w.Code, http.StatusCreated)
	}
}

func TestHandleDeleteTraitLocal(t *testing.T) {
	s := newTraitShim(t, nil, "CUSTOM_DEL")
	w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/CUSTOM_DEL")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	found, err := s.hasTrait(context.Background(), "CUSTOM_DEL")
	if err != nil {
		t.Fatalf("hasTrait: %v", err)
	}
	if found {
		t.Error("expected trait to be deleted")
	}
}

func TestHandleDeleteTraitLocalNotFound(t *testing.T) {
	s := newTraitShim(t, nil)
	w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/CUSTOM_GONE")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteTraitLocalBadPrefix(t *testing.T) {
	s := newTraitShim(t, []string{"HW_CPU"})
	w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/HW_CPU")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

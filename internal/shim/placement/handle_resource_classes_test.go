// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/resourcelock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newTestResourceClassConfigMap(namespace, name string, classes []string) *corev1.ConfigMap {
	b, err := json.Marshal(classes)
	if err != nil {
		panic("marshal resource classes: " + err.Error())
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string]string{configMapKeyResourceClasses: string(b)},
	}
}

func newResourceClassShim(t *testing.T, classes []string) *Shim {
	t.Helper()
	t.Setenv("POD_NAMESPACE", "default")
	objs := []client.Object{newTestResourceClassConfigMap("default", "test-rc-cm", classes)}
	cl := newFakeClientWithScheme(t, objs...)
	down, up := newTestTimers()
	return &Shim{
		Client: cl,
		config: config{
			PlacementURL:    "http://should-not-be-called:1234",
			Features:        featuresConfig{ResourceClasses: FeatureModeCRD},
			ResourceClasses: &resourceClassesConfig{ConfigMapName: "test-rc-cm"},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
		resourceLocker:         resourcelock.NewResourceLocker(cl, "default"),
	}
}

// --- Passthrough mode tests ---

func TestHandleListResourceClassesPassthrough(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"resource_classes":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/resource_classes", s.HandleListResourceClasses, "/resource_classes")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_classes" {
		t.Fatalf("upstream path = %q, want /resource_classes", gotPath)
	}
}

func TestHandleCreateResourceClassPassthrough(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, "{}", nil)
	w := serveHandler(t, "POST", "/resource_classes", s.HandleCreateResourceClass, "/resource_classes")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleShowResourceClassPassthrough(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, "{}", &gotPath)
	w := serveHandler(t, "GET", "/resource_classes/{name}", s.HandleShowResourceClass, "/resource_classes/VCPU")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_classes/VCPU" {
		t.Fatalf("upstream path = %q, want /resource_classes/VCPU", gotPath)
	}
}

func TestHandleUpdateResourceClassPassthrough(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_FOO")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleDeleteResourceClassPassthrough(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/CUSTOM_BAR")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// --- CRD mode handler tests ---

func TestHandleListResourceClassesLocal(t *testing.T) {
	s := newResourceClassShim(t, []string{"CUSTOM_FOO", "MEMORY_MB", "VCPU"})

	w := serveHandler(t, "GET", "/resource_classes", s.HandleListResourceClasses, "/resource_classes")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp resourceClassesListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.ResourceClasses) != 3 {
		t.Fatalf("got %d classes, want 3: %v", len(resp.ResourceClasses), resp.ResourceClasses)
	}
	want := []string{"CUSTOM_FOO", "MEMORY_MB", "VCPU"}
	for i, rc := range resp.ResourceClasses {
		if rc.Name != want[i] {
			t.Errorf("class[%d] = %q, want %q", i, rc.Name, want[i])
		}
		if len(rc.Links) != 1 || rc.Links[0].Rel != "self" || rc.Links[0].Href != "/resource_classes/"+rc.Name {
			t.Errorf("class[%d] links = %v, want self link", i, rc.Links)
		}
	}
}

func TestHandleShowResourceClassLocalFound(t *testing.T) {
	s := newResourceClassShim(t, []string{"VCPU", "MEMORY_MB"})
	w := serveHandler(t, "GET", "/resource_classes/{name}", s.HandleShowResourceClass, "/resource_classes/VCPU")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleShowResourceClassLocalNotFound(t *testing.T) {
	s := newResourceClassShim(t, []string{"VCPU"})
	w := serveHandler(t, "GET", "/resource_classes/{name}", s.HandleShowResourceClass, "/resource_classes/NONEXISTENT")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateResourceClassLocalCreated(t *testing.T) {
	s := newResourceClassShim(t, nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_NEW")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_NEW")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if !found {
		t.Error("expected resource class to be in store")
	}
}

func TestHandleUpdateResourceClassLocalAlreadyExists(t *testing.T) {
	s := newResourceClassShim(t, []string{"CUSTOM_EXISTING"})
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_EXISTING")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleUpdateResourceClassLocalBadPrefix(t *testing.T) {
	s := newResourceClassShim(t, nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/VCPU")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateResourceClassLocalCreated(t *testing.T) {
	s := newResourceClassShim(t, nil)
	body := bytes.NewBufferString(`{"name":"CUSTOM_NEW"}`)
	w := serveHandlerWithBody(t, "POST", "/resource_classes", s.HandleCreateResourceClass, "/resource_classes", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_NEW")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if !found {
		t.Error("expected resource class to be in store")
	}
}

func TestHandleCreateResourceClassLocalConflict(t *testing.T) {
	s := newResourceClassShim(t, []string{"CUSTOM_EXISTING"})
	body := bytes.NewBufferString(`{"name":"CUSTOM_EXISTING"}`)
	w := serveHandlerWithBody(t, "POST", "/resource_classes", s.HandleCreateResourceClass, "/resource_classes", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleCreateResourceClassLocalBadPrefix(t *testing.T) {
	s := newResourceClassShim(t, nil)
	body := bytes.NewBufferString(`{"name":"VCPU"}`)
	w := serveHandlerWithBody(t, "POST", "/resource_classes", s.HandleCreateResourceClass, "/resource_classes", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteResourceClassLocal(t *testing.T) {
	s := newResourceClassShim(t, []string{"CUSTOM_DEL"})
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/CUSTOM_DEL")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_DEL")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if found {
		t.Error("expected resource class to be deleted")
	}
}

func TestHandleDeleteResourceClassLocalNotFound(t *testing.T) {
	s := newResourceClassShim(t, nil)
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/CUSTOM_GONE")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteResourceClassLocalBadPrefix(t *testing.T) {
	s := newResourceClassShim(t, []string{"VCPU"})
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/VCPU")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- Hybrid mode tests ---

func newHybridResourceClassShim(t *testing.T, upstreamStatus int, upstreamBody string, classes []string) *Shim {
	t.Helper()
	t.Setenv("POD_NAMESPACE", "default")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(upstreamStatus)
		if upstreamBody != "" {
			if _, err := w.Write([]byte(upstreamBody)); err != nil {
				t.Errorf("failed to write upstream body: %v", err)
			}
		}
	}))
	t.Cleanup(upstream.Close)
	objs := []client.Object{newTestResourceClassConfigMap("default", "test-rc-cm", classes)}
	cl := newFakeClientWithScheme(t, objs...)
	down, up := newTestTimers()
	return &Shim{
		Client: cl,
		config: config{
			PlacementURL:    upstream.URL,
			Features:        featuresConfig{ResourceClasses: FeatureModeHybrid},
			ResourceClasses: &resourceClassesConfig{ConfigMapName: "test-rc-cm"},
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
		resourceLocker:         resourcelock.NewResourceLocker(cl, "default"),
	}
}

func TestHandleListResourceClassesHybridForwards(t *testing.T) {
	s := newHybridResourceClassShim(t, http.StatusOK, `{"resource_classes":[{"name":"VCPU"}]}`, nil)
	w := serveHandler(t, "GET", "/resource_classes", s.HandleListResourceClasses, "/resource_classes")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUpdateResourceClassHybridUpdatesLocal(t *testing.T) {
	s := newHybridResourceClassShim(t, http.StatusCreated, "", nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_HYB")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_HYB")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if !found {
		t.Error("expected resource class to be added to local configmap in hybrid mode")
	}
}

func TestHandleDeleteResourceClassHybridUpdatesLocal(t *testing.T) {
	s := newHybridResourceClassShim(t, http.StatusNoContent, "", []string{"CUSTOM_DEL"})
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/CUSTOM_DEL")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_DEL")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if found {
		t.Error("expected resource class to be removed from local configmap in hybrid mode")
	}
}

func TestHandleUpdateResourceClassHybridUpstreamFailure(t *testing.T) {
	s := newHybridResourceClassShim(t, http.StatusInternalServerError, "upstream error", nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_FAIL")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	found, err := s.hasResourceClass(context.Background(), "CUSTOM_FAIL")
	if err != nil {
		t.Fatalf("hasResourceClass: %v", err)
	}
	if found {
		t.Error("expected resource class NOT to be added when upstream fails")
	}
}

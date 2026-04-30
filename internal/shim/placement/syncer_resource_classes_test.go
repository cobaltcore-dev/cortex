// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/resourcelock"
	"github.com/gophercloud/gophercloud/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestResourceClassSyncerInitCreatesConfigMap(t *testing.T) {
	cl := newFakeClientWithScheme(t)
	rs := NewResourceClassSyncer(cl, "test-rc", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	if err := rs.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-rc"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyResourceClasses] != "[]" {
		t.Fatalf("expected empty resource classes array, got %q", cm.Data[configMapKeyResourceClasses])
	}
}

func TestResourceClassSyncerInitIdempotent(t *testing.T) {
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Data:       map[string]string{configMapKeyResourceClasses: `["CUSTOM_EXISTING"]`},
	}
	cl := newFakeClientWithScheme(t, existing)
	rs := NewResourceClassSyncer(cl, "test-rc", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	if err := rs.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-rc"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyResourceClasses] != `["CUSTOM_EXISTING"]` {
		t.Fatalf("Init overwrote existing data: got %q", cm.Data[configMapKeyResourceClasses])
	}
}

func TestResourceClassSyncerRunNoClient(t *testing.T) {
	cl := newFakeClientWithScheme(t)
	rs := NewResourceClassSyncer(cl, "test-rc", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rs.Run(ctx)
}

func TestResourceClassSyncerSyncWritesUpstreamClasses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resource_classes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resourceClassesListResponse{
			ResourceClasses: []resourceClassEntry{
				{Name: "VCPU"},
				{Name: "MEMORY_MB"},
				{Name: "CUSTOM_SYNCED"},
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Data:       map[string]string{configMapKeyResourceClasses: "[]"},
	}
	cl := newFakeClientWithScheme(t, existing)

	sc := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       upstream.URL,
	}
	sc.HTTPClient = *upstream.Client()

	rs := NewResourceClassSyncer(cl, "test-rc", "default", sc, resourcelock.NewResourceLocker(cl, "default"))
	rs.sync(context.Background())

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-rc"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}

	var classes []string
	if err := json.Unmarshal([]byte(cm.Data[configMapKeyResourceClasses]), &classes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(classes) != 3 {
		t.Fatalf("expected 3 classes, got %d: %v", len(classes), classes)
	}
	want := map[string]bool{"VCPU": true, "MEMORY_MB": true, "CUSTOM_SYNCED": true}
	for _, c := range classes {
		if !want[c] {
			t.Errorf("unexpected class: %s", c)
		}
	}
}

func TestResourceClassSyncerSyncUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Data:       map[string]string{configMapKeyResourceClasses: `["CUSTOM_ORIGINAL"]`},
	}
	cl := newFakeClientWithScheme(t, existing)

	sc := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       upstream.URL,
	}
	sc.HTTPClient = *upstream.Client()

	rs := NewResourceClassSyncer(cl, "test-rc", "default", sc, resourcelock.NewResourceLocker(cl, "default"))
	rs.sync(context.Background())

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-rc"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyResourceClasses] != `["CUSTOM_ORIGINAL"]` {
		t.Fatalf("sync should not have modified ConfigMap on error, got %q", cm.Data[configMapKeyResourceClasses])
	}
}

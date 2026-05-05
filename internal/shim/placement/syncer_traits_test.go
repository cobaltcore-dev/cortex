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

func TestTraitSyncerInitCreatesConfigMap(t *testing.T) {
	cl := newFakeClientWithScheme(t)
	ts := NewTraitSyncer(cl, "test-traits", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	if err := ts.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-traits"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyTraits] != "[]" {
		t.Fatalf("expected empty traits array, got %q", cm.Data[configMapKeyTraits])
	}
}

func TestTraitSyncerInitIdempotent(t *testing.T) {
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-traits", Namespace: "default"},
		Data:       map[string]string{configMapKeyTraits: `["CUSTOM_EXISTING"]`},
	}
	cl := newFakeClientWithScheme(t, existing)
	ts := NewTraitSyncer(cl, "test-traits", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	if err := ts.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-traits"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyTraits] != `["CUSTOM_EXISTING"]` {
		t.Fatalf("Init overwrote existing data: got %q", cm.Data[configMapKeyTraits])
	}
}

func TestTraitSyncerRunNoClient(t *testing.T) {
	cl := newFakeClientWithScheme(t)
	ts := NewTraitSyncer(cl, "test-traits", "default", nil, resourcelock.NewResourceLocker(cl, "default"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ts.Run(ctx)
}

func TestTraitSyncerSyncWritesUpstreamTraits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/traits" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(traitsListResponse{
			Traits: []string{"HW_CPU_X86_AVX2", "CUSTOM_SYNCED"},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-traits", Namespace: "default"},
		Data:       map[string]string{configMapKeyTraits: "[]"},
	}
	cl := newFakeClientWithScheme(t, existing)

	sc := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       upstream.URL,
	}
	sc.HTTPClient = *upstream.Client()

	ts := NewTraitSyncer(cl, "test-traits", "default", sc, resourcelock.NewResourceLocker(cl, "default"))
	ts.sync(context.Background())

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-traits"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}

	var traits []string
	if err := json.Unmarshal([]byte(cm.Data[configMapKeyTraits]), &traits); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(traits) != 2 {
		t.Fatalf("expected 2 traits, got %d: %v", len(traits), traits)
	}
	want := map[string]bool{"CUSTOM_SYNCED": true, "HW_CPU_X86_AVX2": true}
	for _, tr := range traits {
		if !want[tr] {
			t.Errorf("unexpected trait: %s", tr)
		}
	}
}

func TestTraitSyncerSyncUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-traits", Namespace: "default"},
		Data:       map[string]string{configMapKeyTraits: `["CUSTOM_ORIGINAL"]`},
	}
	cl := newFakeClientWithScheme(t, existing)

	sc := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       upstream.URL,
	}
	sc.HTTPClient = *upstream.Client()

	ts := NewTraitSyncer(cl, "test-traits", "default", sc, resourcelock.NewResourceLocker(cl, "default"))
	ts.sync(context.Background())

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-traits"}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[configMapKeyTraits] != `["CUSTOM_ORIGINAL"]` {
		t.Fatalf("sync should not have modified ConfigMap on error, got %q", cm.Data[configMapKeyTraits])
	}
}

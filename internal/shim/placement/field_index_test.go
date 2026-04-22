// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type indexCall struct {
	field        string
	extractValue client.IndexerFunc
}

type captureCache struct {
	cache.Cache
	calls []indexCall
	mu    sync.Mutex
	err   error
}

func (c *captureCache) IndexField(_ context.Context, _ client.Object, field string, fn client.IndexerFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, indexCall{field: field, extractValue: fn})
	return c.err
}

type stubCluster struct {
	cluster.Cluster
	cl    client.Client
	cache *captureCache
}

func (s *stubCluster) GetClient() client.Client { return s.cl }
func (s *stubCluster) GetCache() cache.Cache    { return s.cache }

type stubManager struct {
	manager.Manager
}

func (m *stubManager) Add(manager.Runnable) error { return nil }

func fieldIndexScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("hv1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	return s
}

func buildClient(t *testing.T, cc *captureCache) *multicluster.Client {
	t.Helper()
	s := fieldIndexScheme(t)
	home := &stubCluster{
		cl:    fake.NewClientBuilder().WithScheme(s).Build(),
		cache: cc,
	}
	mcl := &multicluster.Client{
		HomeCluster: home,
		HomeScheme:  s,
	}
	conf := multicluster.ClientConfig{
		APIServers: multicluster.APIServersConfig{
			Home: multicluster.HomeConfig{
				GVKs: []string{
					"kvm.cloud.sap/v1/Hypervisor",
					"kvm.cloud.sap/v1/HypervisorList",
				},
			},
		},
	}
	if err := mcl.InitFromConf(context.Background(), &stubManager{}, conf); err != nil {
		t.Fatalf("InitFromConf: %v", err)
	}
	return mcl
}

func extractorByField(t *testing.T, calls []indexCall, field string) client.IndexerFunc {
	t.Helper()
	for _, c := range calls {
		if c.field == field {
			return c.extractValue
		}
	}
	t.Fatalf("no IndexField call for field %q", field)
	return nil
}

func TestIndexFields_RegistersAllIndexes(t *testing.T) {
	cc := &captureCache{}
	mcl := buildClient(t, cc)

	if err := indexFields(context.Background(), mcl); err != nil {
		t.Fatalf("indexFields: %v", err)
	}

	wantFields := []string{
		idxHypervisorOpenStackId,
		idxHypervisorKubernetesId,
		idxHypervisorName,
	}
	if len(cc.calls) != len(wantFields) {
		t.Fatalf("got %d IndexField calls, want %d", len(cc.calls), len(wantFields))
	}
	got := make(map[string]bool)
	for _, c := range cc.calls {
		got[c.field] = true
	}
	for _, f := range wantFields {
		if !got[f] {
			t.Errorf("missing IndexField call for %q", f)
		}
	}
}

func TestIndexFields_PropagatesError(t *testing.T) {
	wantErr := errors.New("cache failure")
	cc := &captureCache{err: wantErr}
	mcl := buildClient(t, cc)

	err := indexFields(context.Background(), mcl)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestExtractor_HypervisorOpenStackId(t *testing.T) {
	cc := &captureCache{}
	mcl := buildClient(t, cc)
	if err := indexFields(context.Background(), mcl); err != nil {
		t.Fatalf("indexFields: %v", err)
	}
	fn := extractorByField(t, cc.calls, idxHypervisorOpenStackId)

	tests := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{
			name: "populated ID",
			obj: &hv1.Hypervisor{
				Status: hv1.HypervisorStatus{HypervisorID: "os-123"},
			},
			want: []string{"os-123"},
		},
		{
			name: "empty ID",
			obj:  &hv1.Hypervisor{},
			want: nil,
		},
		{
			name: "wrong type",
			obj:  &corev1.ConfigMap{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fn(tt.obj)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractor_HypervisorKubernetesId(t *testing.T) {
	cc := &captureCache{}
	mcl := buildClient(t, cc)
	if err := indexFields(context.Background(), mcl); err != nil {
		t.Fatalf("indexFields: %v", err)
	}
	fn := extractorByField(t, cc.calls, idxHypervisorKubernetesId)

	tests := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{
			name: "populated UID",
			obj: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-456")},
			},
			want: []string{"uid-456"},
		},
		{
			name: "empty UID",
			obj:  &hv1.Hypervisor{},
			want: []string{""},
		},
		{
			name: "wrong type",
			obj:  &corev1.ConfigMap{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fn(tt.obj)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractor_HypervisorName(t *testing.T) {
	cc := &captureCache{}
	mcl := buildClient(t, cc)
	if err := indexFields(context.Background(), mcl); err != nil {
		t.Fatalf("indexFields: %v", err)
	}
	fn := extractorByField(t, cc.calls, idxHypervisorName)

	tests := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{
			name: "populated name",
			obj: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "hv-node-01"},
			},
			want: []string{"hv-node-01"},
		},
		{
			name: "empty name",
			obj:  &hv1.Hypervisor{},
			want: []string{""},
		},
		{
			name: "wrong type",
			obj:  &corev1.ConfigMap{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fn(tt.obj)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// unversionedType is a type that is registered as unversioned in the scheme.
type unversionedType struct {
	metav1.TypeMeta `json:",inline"`
}

func (u *unversionedType) DeepCopyObject() runtime.Object {
	return &unversionedType{TypeMeta: u.TypeMeta}
}

// unknownType is a type that is NOT registered in the scheme.
type unknownType struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func (u *unknownType) DeepCopyObject() runtime.Object {
	return &unknownType{TypeMeta: u.TypeMeta, ObjectMeta: u.ObjectMeta}
}

// fakeCache implements cache.Cache interface for testing IndexField.
type fakeCache struct {
	cache.Cache
	indexFieldFunc  func(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error
	indexFieldCalls []indexFieldCall
	mu              sync.Mutex
}

type indexFieldCall struct {
	obj   client.Object
	field string
}

func (f *fakeCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	f.mu.Lock()
	f.indexFieldCalls = append(f.indexFieldCalls, indexFieldCall{obj: obj, field: field})
	f.mu.Unlock()
	if f.indexFieldFunc != nil {
		return f.indexFieldFunc(ctx, obj, field, extractValue)
	}
	return nil
}

func (f *fakeCache) getIndexFieldCalls() []indexFieldCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.indexFieldCalls
}

// fakeCluster implements cluster.Cluster interface for testing.
type fakeCluster struct {
	cluster.Cluster
	fakeClient client.Client
	fakeCache  *fakeCache
}

func (f *fakeCluster) GetClient() client.Client {
	return f.fakeClient
}

func (f *fakeCluster) GetCache() cache.Cache {
	return f.fakeCache
}

func newFakeCluster(scheme *runtime.Scheme, objs ...client.Object) *fakeCluster {
	return &fakeCluster{
		fakeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		fakeCache:  &fakeCache{},
	}
}

//nolint:unparam
func newFakeClusterWithCache(scheme *runtime.Scheme, fakeCache *fakeCache, objs ...client.Object) *fakeCluster {
	return &fakeCluster{
		fakeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		fakeCache:  fakeCache,
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	return scheme
}

// testRouter is a simple ResourceRouter for testing.
type testRouter struct{}

func (r testRouter) Match(obj any, labels map[string]string) (bool, error) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return false, nil
	}
	az, ok := labels["az"]
	if !ok {
		return false, nil
	}
	objAZ, ok := cm.Labels["az"]
	if !ok {
		return false, nil
	}
	return objAZ == az, nil
}

var configMapGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
var configMapListGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}
var podGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}

func TestClient_Apply(t *testing.T) {
	c := &Client{HomeScheme: newTestScheme(t)}

	// Check if apply will throw an error since it's not supported by multicluster client.
	err := c.Apply(context.Background(), nil)
	if err == nil {
		t.Error("expected error for Apply operation")
	}
}

func TestStatusClient_Apply(t *testing.T) {
	sc := &statusClient{multiclusterClient: &Client{}}

	// Check if apply will throw an error since it's not supported by multicluster client.
	err := sc.Apply(context.Background(), nil)
	if err == nil {
		t.Error("expected error for Apply operation")
	}
}

func TestSubResourceClient_Apply(t *testing.T) {
	src := &subResourceClient{multiclusterClient: &Client{}, subResource: "status"}

	// Check if apply will throw an error since it's not supported by multicluster client.
	err := src.Apply(context.Background(), nil)
	if err == nil {
		t.Error("expected error for Apply operation")
	}
}

func TestClient_ClustersForGVK_HomeGVKOnly(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{configMapGVK: true},
	}

	clusters, err := c.ClustersForGVK(configMapGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0] != homeCluster {
		t.Error("expected home cluster")
	}
}

func TestClient_ClustersForGVK_UnknownGVK(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}

	_, err := c.ClustersForGVK(configMapGVK)
	if err == nil {
		t.Error("expected error for unknown GVK")
	}
}

func TestClient_ClustersForGVK_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote, labels: map[string]string{"az": "az-1"}}},
		},
	}

	clusters, err := c.ClustersForGVK(configMapGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0] != remote {
		t.Error("expected remote cluster")
	}
}

func TestClient_ClustersForGVK_MultipleRemoteClusters(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote1 := newFakeCluster(scheme)
	remote2 := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: remote2, labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	clusters, err := c.ClustersForGVK(configMapGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}
}

func TestClient_ClustersForGVK_HomeAndRemote(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote, labels: map[string]string{"az": "az-1"}}},
		},
		homeGVKs: map[schema.GroupVersionKind]bool{configMapGVK: true},
	}

	clusters, err := c.ClustersForGVK(configMapGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters (remote + home), got %d", len(clusters))
	}
	if clusters[0] != remote {
		t.Error("expected remote cluster first")
	}
	if clusters[1] != homeCluster {
		t.Error("expected home cluster second")
	}
}

func TestClient_clusterForWrite_HomeGVK(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{configMapGVK: true},
	}

	cl, err := c.clusterForWrite(configMapGVK, &corev1.ConfigMap{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cl != homeCluster {
		t.Error("expected home cluster for home GVK")
	}
}

func TestClient_clusterForWrite_UnknownGVK(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}

	_, err := c.clusterForWrite(configMapGVK, &corev1.ConfigMap{})
	if err == nil {
		t.Error("expected error for unknown GVK")
	}
}

func TestClient_clusterForWrite_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote, labels: map[string]string{"az": "az-1"}}},
		},
	}

	cl, err := c.clusterForWrite(configMapGVK, &corev1.ConfigMap{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cl != remote {
		t.Error("expected remote cluster for single remote")
	}
}

func TestClient_clusterForWrite_RouterMatches(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote1 := newFakeCluster(scheme)
	remote2 := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			configMapGVK: testRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: remote2, labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"az": "az-2"}},
	}
	cl, err := c.clusterForWrite(configMapGVK, obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cl != remote2 {
		t.Error("expected second remote cluster for az-2")
	}
}

func TestClient_clusterForWrite_NoMatch(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote1 := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			configMapGVK: testRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: newFakeCluster(scheme), labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"az": "az-3"}},
	}
	_, err := c.clusterForWrite(configMapGVK, obj)
	if err == nil {
		t.Error("expected error when no remote cluster matches")
	}
}

func TestClient_clusterForWrite_NoRouterMultipleClusters(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: newFakeCluster(scheme), labels: map[string]string{"az": "az-1"}},
				{cluster: newFakeCluster(scheme), labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	_, err := c.clusterForWrite(configMapGVK, &corev1.ConfigMap{})
	if err == nil {
		t.Error("expected error when no router with multiple clusters")
	}
}

func TestGVKFromHomeScheme_Success(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{HomeScheme: scheme}

	tests := []struct {
		name        string
		obj         runtime.Object
		expectedGVK schema.GroupVersionKind
	}{
		{"ConfigMap", &corev1.ConfigMap{}, configMapGVK},
		{"ConfigMapList", &corev1.ConfigMapList{}, configMapListGVK},
		{"Decision", &v1alpha1.Decision{}, schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Decision"}},
		{"DecisionList", &v1alpha1.DecisionList{}, schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "DecisionList"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvk, err := c.GVKFromHomeScheme(tt.obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gvk != tt.expectedGVK {
				t.Errorf("expected GVK %v, got %v", tt.expectedGVK, gvk)
			}
		})
	}
}

func TestGVKFromHomeScheme_UnknownType(t *testing.T) {
	c := &Client{HomeScheme: newTestScheme(t)}
	_, err := c.GVKFromHomeScheme(&unknownType{})
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestGVKFromHomeScheme_UnversionedType(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddUnversionedTypes(schema.GroupVersion{Group: "", Version: "v1"}, &unversionedType{})
	c := &Client{HomeScheme: scheme}
	_, err := c.GVKFromHomeScheme(&unversionedType{})
	if err == nil {
		t.Error("expected error for unversioned type")
	}
}

func TestGVKFromHomeScheme_NilScheme(t *testing.T) {
	c := &Client{HomeScheme: nil}
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil scheme")
		}
	}()
	if _, err := c.GVKFromHomeScheme(&corev1.ConfigMap{}); err == nil {
		t.Error("expected panic with nil scheme")
	}
}

func TestClient_Get_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: "default"},
		Data:       map[string]string{"key": "remote-value"},
	}
	remote := newFakeCluster(scheme, existingCM)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "test-cm", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Data["key"] != "remote-value" {
		t.Errorf("expected 'remote-value', got '%s'", cm.Data["key"])
	}
}

func TestClient_Get_MultiCluster_FirstFound(t *testing.T) {
	// Iterate all remote clusters and return the first found object. In this test, only remote2 has the object, so it should be returned.

	scheme := newTestScheme(t)
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: "default"},
		Data:       map[string]string{"key": "from-cluster-2"},
	}
	remote1 := newFakeCluster(scheme) // empty
	remote2 := newFakeCluster(scheme, existingCM)

	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: remote2, labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "test-cm", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Data["key"] != "from-cluster-2" {
		t.Errorf("expected 'from-cluster-2', got '%s'", cm.Data["key"])
	}
}

func TestClient_Get_MultiCluster_NotFound(t *testing.T) {
	// Iterate all remote clusters and return NotFound if object is not found in any cluster.
	// In this test, the object doesn't exist in any cluster, so NotFound should be returned.

	scheme := newTestScheme(t)
	remote1 := newFakeCluster(scheme) // empty
	remote2 := newFakeCluster(scheme) // empty

	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1},
				{cluster: remote2},
			},
		},
	}

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "missing", Namespace: "default"}, cm)
	if err == nil {
		t.Error("expected NotFound error")
	}
}

func TestClient_Get_UnknownType(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}
	obj := &unknownType{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
	err := c.Get(context.Background(), client.ObjectKey{Name: "test", Namespace: "default"}, obj)
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestClient_Get_HomeGVKCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "home-cm", Namespace: "default"},
		Data:       map[string]string{"key": "from-home"},
	})
	remote := newFakeCluster(scheme) // empty

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
		homeGVKs: map[schema.GroupVersionKind]bool{configMapGVK: true},
	}

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "home-cm", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Data["key"] != "from-home" {
		t.Errorf("expected 'from-home', got '%s'", cm.Data["key"])
	}
}

func TestClient_List_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	cm1 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: "default"}}
	cm2 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm2", Namespace: "default"}}
	remote := newFakeCluster(scheme, cm1, cm2)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapListGVK: {{cluster: remote}},
		},
	}

	cmList := &corev1.ConfigMapList{}
	err := c.List(context.Background(), cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(cmList.Items))
	}
}

func TestClient_List_MultipleClusters_CombinesResults(t *testing.T) {
	scheme := newTestScheme(t)
	remote1 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-az1-1", Namespace: "default"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-az1-2", Namespace: "default"}},
	)
	remote2 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-az2-1", Namespace: "default"}},
	)

	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapListGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: remote2, labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	cmList := &corev1.ConfigMapList{}
	err := c.List(context.Background(), cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 3 {
		t.Errorf("expected 3 combined items, got %d", len(cmList.Items))
	}
}

func TestClient_List_HomeGVKIncludesHome(t *testing.T) {
	scheme := newTestScheme(t)
	remote := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "remote-cm", Namespace: "default"}},
	)
	homeCluster := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "home-cm", Namespace: "default"}},
	)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapListGVK: {{cluster: remote}},
		},
		homeGVKs: map[schema.GroupVersionKind]bool{configMapListGVK: true},
	}

	cmList := &corev1.ConfigMapList{}
	err := c.List(context.Background(), cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 2 {
		t.Errorf("expected 2 items (remote + home), got %d", len(cmList.Items))
	}
}

func TestClient_List_HomeClusterOnly(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "home-cm", Namespace: "default"}},
	)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{configMapListGVK: true},
	}

	cmList := &corev1.ConfigMapList{}
	err := c.List(context.Background(), cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(cmList.Items))
	}
}

func TestClient_Create_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "new-cm", Namespace: "default"},
	}
	err := c.Create(context.Background(), cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was created in remote, not home.
	result := &corev1.ConfigMap{}
	err = remote.GetClient().Get(context.Background(), client.ObjectKey{Name: "new-cm", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("expected to find in remote cluster: %v", err)
	}
}

func TestClient_Create_RouterMatchesCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remote1 := newFakeCluster(scheme)
	remote2 := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			configMapGVK: testRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1, labels: map[string]string{"az": "az-1"}},
				{cluster: remote2, labels: map[string]string{"az": "az-2"}},
			},
		},
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-cm",
			Namespace: "default",
			Labels:    map[string]string{"az": "az-2"},
		},
	}
	err := c.Create(context.Background(), cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be in remote2, not remote1.
	result := &corev1.ConfigMap{}
	err = remote2.GetClient().Get(context.Background(), client.ObjectKey{Name: "new-cm", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("expected to find in remote2: %v", err)
	}
	err = remote1.GetClient().Get(context.Background(), client.ObjectKey{Name: "new-cm", Namespace: "default"}, result)
	if err == nil {
		t.Error("should NOT be in remote1")
	}
}

func TestClient_Create_NoMatchReturnsError(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			configMapGVK: testRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: newFakeCluster(scheme), labels: map[string]string{"az": "az-1"}},
			},
		},
	}

	// Object with az-99 doesn't match any remote — should error.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-match-cm",
			Namespace: "default",
			Labels:    map[string]string{"az": "az-99"},
		},
	}

	err := c.Create(context.Background(), cm)
	if err == nil {
		t.Error("expected error when no remote cluster matches")
	}
}

func TestClient_Delete_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "to-delete", Namespace: "default"},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, existingCM)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "to-delete", Namespace: "default"}}
	err := c.Delete(context.Background(), cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := &corev1.ConfigMap{}
	err = remote.GetClient().Get(context.Background(), client.ObjectKey{Name: "to-delete", Namespace: "default"}, result)
	if err == nil {
		t.Error("expected object to be deleted from remote cluster")
	}
}

func TestClient_Update_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "to-update", Namespace: "default"},
		Data:       map[string]string{"key": "old-value"},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, existingCM)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	ctx := context.Background()
	cm := &corev1.ConfigMap{}
	err := remote.GetClient().Get(ctx, client.ObjectKey{Name: "to-update", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}

	cm.Data["key"] = "new-value"
	err = c.Update(ctx, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := &corev1.ConfigMap{}
	err = remote.GetClient().Get(ctx, client.ObjectKey{Name: "to-update", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get updated object: %v", err)
	}
	if result.Data["key"] != "new-value" {
		t.Errorf("expected 'new-value', got '%s'", result.Data["key"])
	}
}

func TestClient_Patch_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "to-patch", Namespace: "default"},
		Data:       map[string]string{"key": "old-value"},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, existingCM)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	ctx := context.Background()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "to-patch", Namespace: "default"}}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"key": "patched-value"}
	err := c.Patch(ctx, cm, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := &corev1.ConfigMap{}
	err = remote.GetClient().Get(ctx, client.ObjectKey{Name: "to-patch", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}
	if result.Data["key"] != "patched-value" {
		t.Errorf("expected 'patched-value', got '%s'", result.Data["key"])
	}
}

func TestClient_DeleteAllOf_SingleRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: "default", Labels: map[string]string{"app": "test"}},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm2", Namespace: "default", Labels: map[string]string{"app": "test"}},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, cm1, cm2)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {{cluster: remote}},
		},
	}

	err := c.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, client.InNamespace("default"), client.MatchingLabels{"app": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmList := &corev1.ConfigMapList{}
	err = remote.GetClient().List(context.Background(), cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("failed to list objects: %v", err)
	}
	if len(cmList.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(cmList.Items))
	}
}

func TestClient_DeleteAllOf_MultipleClusters(t *testing.T) {
	scheme := newTestScheme(t)
	remote1 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: "default", Labels: map[string]string{"app": "test"}}},
	)
	remote2 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm2", Namespace: "default", Labels: map[string]string{"app": "test"}}},
	)

	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK: {
				{cluster: remote1},
				{cluster: remote2},
			},
		},
	}

	err := c.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, client.InNamespace("default"), client.MatchingLabels{"app": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, remote := range []*fakeCluster{remote1, remote2} {
		cmList := &corev1.ConfigMapList{}
		if err := remote.GetClient().List(context.Background(), cmList, client.InNamespace("default")); err != nil {
			t.Fatalf("failed to list objects in remote%d: %v", i+1, err)
		}

		if len(cmList.Items) != 0 {
			t.Errorf("expected 0 items in remote%d, got %d", i+1, len(cmList.Items))
		}
	}
}

func TestStatusClient_Update(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	homeCluster := newFakeCluster(scheme, pod)
	c := &Client{HomeCluster: homeCluster, HomeScheme: scheme, homeGVKs: map[schema.GroupVersionKind]bool{podGVK: true}}

	ctx := context.Background()
	existingPod := &corev1.Pod{}
	if err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod); err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}
	existingPod.Status.Phase = corev1.PodRunning
	if err := c.Status().Update(ctx, existingPod); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate the update took effect
	result := &corev1.Pod{}
	if err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, result); err != nil {
		t.Fatalf("failed to get updated pod: %v", err)
	}
	if result.Status.Phase != corev1.PodRunning {
		t.Errorf("expected PodRunning, got %s", result.Status.Phase)
	}
}

func TestStatusClient_Patch(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	homeCluster := newFakeCluster(scheme, pod)
	c := &Client{HomeCluster: homeCluster, HomeScheme: scheme, homeGVKs: map[schema.GroupVersionKind]bool{podGVK: true}}

	ctx := context.Background()
	existingPod := &corev1.Pod{}
	if err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod); err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}
	patch := client.MergeFrom(existingPod.DeepCopy())
	existingPod.Status.Phase = corev1.PodRunning
	if err := c.Status().Patch(ctx, existingPod, patch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate the patch took effect
	result := &corev1.Pod{}
	if err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, result); err != nil {
		t.Fatalf("failed to get patched pod: %v", err)
	}
	if result.Status.Phase != corev1.PodRunning {
		t.Errorf("expected PodRunning, got %s", result.Status.Phase)
	}
}

func TestStatusClient_RoutesToRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			podGVK: {{cluster: remote}},
		},
	}

	ctx := context.Background()
	existingPod := &corev1.Pod{}
	if err := remote.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod); err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}
	existingPod.Status.Phase = corev1.PodRunning
	if err := c.Status().Update(ctx, existingPod); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := &corev1.Pod{}
	if err := remote.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, result); err != nil {
		t.Fatalf("failed to get updated pod: %v", err)
	}
	if result.Status.Phase != corev1.PodRunning {
		t.Errorf("expected PodRunning, got %s", result.Status.Phase)
	}
}

func TestClient_StatusAndSubResource_ErrorOnUnknownType(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{HomeCluster: newFakeCluster(scheme), HomeScheme: scheme}
	ctx := context.Background()
	obj := &unknownType{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}

	if err := c.Status().Update(ctx, obj); err == nil {
		t.Error("expected error for unknown type in status Update")
	}
	if err := c.Status().Patch(ctx, obj, client.MergeFrom(obj)); err == nil {
		t.Error("expected error for unknown type in status Patch")
	}
	if err := c.SubResource("status").Update(ctx, obj); err == nil {
		t.Error("expected error for unknown type in subresource Update")
	}
	if err := c.SubResource("status").Patch(ctx, obj, client.MergeFrom(obj)); err == nil {
		t.Error("expected error for unknown type in subresource Patch")
	}
}

func TestSubResourceClient_RoutesToRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
	}
	homeCluster := newFakeCluster(scheme)
	remote := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			podGVK: {{cluster: remote}},
		},
	}

	ctx := context.Background()
	existingPod := &corev1.Pod{}
	if err := remote.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod); err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}
	src := c.SubResource("status")
	if err := src.Update(ctx, existingPod); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_GroupVersionKindFor(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{HomeCluster: newFakeCluster(scheme), HomeScheme: scheme}
	gvk, err := c.GroupVersionKindFor(&corev1.ConfigMap{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gvk != configMapGVK {
		t.Errorf("expected GVK %v, got %v", configMapGVK, gvk)
	}
}

func TestClient_IndexField_WithRemoteClusters(t *testing.T) {
	scheme := newTestScheme(t)
	homeCache := &fakeCache{}
	homeCluster := newFakeClusterWithCache(scheme, homeCache)
	remoteObjCache := &fakeCache{}
	remoteObjCluster := newFakeClusterWithCache(scheme, remoteObjCache)
	remoteListCache := &fakeCache{}
	remoteListCluster := newFakeClusterWithCache(scheme, remoteListCache)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK:     {{cluster: remoteObjCluster}},
			configMapListGVK: {{cluster: remoteListCluster}},
		},
	}

	err := c.IndexField(context.Background(), &corev1.ConfigMap{}, &corev1.ConfigMapList{}, "metadata.name", func(obj client.Object) []string {
		return []string{obj.GetName()}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remoteObjCache.getIndexFieldCalls()) != 1 {
		t.Errorf("expected 1 IndexField call on remote obj cache, got %d", len(remoteObjCache.getIndexFieldCalls()))
	}
	if len(remoteListCache.getIndexFieldCalls()) != 1 {
		t.Errorf("expected 1 IndexField call on remote list cache, got %d", len(remoteListCache.getIndexFieldCalls()))
	}
	if len(homeCache.getIndexFieldCalls()) != 0 {
		t.Errorf("expected 0 IndexField calls on home cache, got %d", len(homeCache.getIndexFieldCalls()))
	}
}

func TestClient_IndexField_SameClusterSkipsDuplicate(t *testing.T) {
	scheme := newTestScheme(t)
	remoteCache := &fakeCache{}
	remote := newFakeClusterWithCache(scheme, remoteCache)
	homeCache := &fakeCache{}
	homeCluster := newFakeClusterWithCache(scheme, homeCache)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			configMapGVK:     {{cluster: remote}},
			configMapListGVK: {{cluster: remote}}, // same cluster
		},
	}

	err := c.IndexField(context.Background(), &corev1.ConfigMap{}, &corev1.ConfigMapList{}, "metadata.name", func(obj client.Object) []string {
		return []string{obj.GetName()}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remoteCache.getIndexFieldCalls()) != 1 {
		t.Errorf("expected 1 IndexField call (deduped), got %d", len(remoteCache.getIndexFieldCalls()))
	}
	if len(homeCache.getIndexFieldCalls()) != 0 {
		t.Errorf("expected 0 IndexField calls on home, got %d", len(homeCache.getIndexFieldCalls()))
	}
}

func TestClient_IndexField_HomeClusterOnly(t *testing.T) {
	scheme := newTestScheme(t)
	homeCache := &fakeCache{}
	homeCluster := newFakeClusterWithCache(scheme, homeCache)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs: map[schema.GroupVersionKind]bool{
			configMapGVK:     true,
			configMapListGVK: true,
		},
	}

	err := c.IndexField(context.Background(), &corev1.ConfigMap{}, &corev1.ConfigMapList{}, "metadata.name", func(obj client.Object) []string {
		return []string{obj.GetName()}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(homeCache.getIndexFieldCalls()) != 1 {
		t.Errorf("expected 1 IndexField call on home (deduped), got %d", len(homeCache.getIndexFieldCalls()))
	}
}

func TestClient_ConcurrentAddRemoteAndRead(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster:    newFakeCluster(scheme),
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{},
	}

	var wg sync.WaitGroup

	// Readers
	for range 10 {
		wg.Go(func() {
			for range 100 {
				_, _ = c.ClustersForGVK(configMapGVK)
			}
		})
	}

	// Writers
	for range 5 {
		wg.Go(func() {
			for range 100 {
				c.remoteClustersMu.Lock()
				c.remoteClusters[configMapGVK] = append(c.remoteClusters[configMapGVK], remoteCluster{})
				c.remoteClustersMu.Unlock()
			}
		})
	}

	wg.Wait()
}

// fakeManager implements ctrl.Manager for testing InitFromConf.
type fakeManager struct {
	ctrl.Manager
	addedRunnables []cluster.Cluster
	addError       error
}

func (f *fakeManager) Add(runnable manager.Runnable) error {
	if f.addError != nil {
		return f.addError
	}
	if cl, ok := runnable.(cluster.Cluster); ok {
		f.addedRunnables = append(f.addedRunnables, cl)
	}
	return nil
}

func TestClient_InitFromConf_EmptyConfig(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}
	mgr := &fakeManager{}

	err := c.InitFromConf(context.Background(), mgr, ClientConfig{})
	if err != nil {
		t.Fatalf("unexpected error with empty config: %v", err)
	}
	if len(mgr.addedRunnables) != 0 {
		t.Errorf("expected 0 runnables added, got %d", len(mgr.addedRunnables))
	}
}

func TestClient_InitFromConf_UnregisteredRemoteGVK(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}
	mgr := &fakeManager{}
	conf := ClientConfig{
		APIServers: APIServersConfig{
			Remotes: []RemoteConfig{
				{
					Host: "https://remote-api:6443",
					GVKs: []string{"unregistered.group/v1/UnknownKind"},
				},
			},
		},
	}

	err := c.InitFromConf(context.Background(), mgr, conf)
	if err == nil {
		t.Fatal("expected error for unregistered remote GVK")
	}
}

func TestClient_InitFromConf_UnregisteredHomeGVK(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Client{
		HomeCluster: newFakeCluster(scheme),
		HomeScheme:  scheme,
	}
	mgr := &fakeManager{}
	conf := ClientConfig{
		APIServers: APIServersConfig{
			Home: HomeConfig{GVKs: []string{"unregistered.group/v1/UnknownKind"}},
		},
	}

	err := c.InitFromConf(context.Background(), mgr, conf)
	if err == nil {
		t.Fatal("expected error for unregistered home GVK")
	}
}

func TestClient_InitFromConf_GVKFormatting(t *testing.T) {
	scheme := newTestScheme(t)
	tests := []struct {
		name        string
		gvk         schema.GroupVersionKind
		expectedStr string
	}{
		{"core ConfigMap", configMapGVK, "v1/ConfigMap"},
		{"cortex Decision", schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Decision"}, "cortex.cloud/v1alpha1/Decision"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, found := scheme.AllKnownTypes()[tt.gvk]; !found {
				t.Skipf("GVK %v not in scheme", tt.gvk)
			}
			formatted := tt.gvk.GroupVersion().String() + "/" + tt.gvk.Kind
			if formatted != tt.expectedStr {
				t.Errorf("expected '%s', got '%s'", tt.expectedStr, formatted)
			}
		})
	}
}

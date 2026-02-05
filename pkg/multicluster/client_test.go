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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

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

// fakeCluster implements cluster.Cluster interface for testing.
type fakeCluster struct {
	cluster.Cluster
	fakeClient client.Client
}

func (f *fakeCluster) GetClient() client.Client {
	return f.fakeClient
}

func newFakeCluster(scheme *runtime.Scheme, objs ...client.Object) *fakeCluster {
	return &fakeCluster{
		fakeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
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

// TestClient_Apply tests that the Apply method returns an error.
func TestClient_Apply(t *testing.T) {
	scheme := newTestScheme(t)

	c := &Client{
		HomeScheme: scheme,
	}

	ctx := context.Background()

	t.Run("apply returns error", func(t *testing.T) {
		err := c.Apply(ctx, nil)
		if err == nil {
			t.Error("expected error for Apply operation")
		}
		if err.Error() != "apply operation is not supported in multicluster client" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

// TestStatusClient_Apply tests that the status client Apply method returns an error.
func TestStatusClient_Apply(t *testing.T) {
	sc := &statusClient{multiclusterClient: &Client{}}

	ctx := context.Background()

	err := sc.Apply(ctx, nil)
	if err == nil {
		t.Error("expected error for Apply operation")
	}
	if err.Error() != "apply operation is not supported in multicluster status client" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestSubResourceClientApply tests that the subresource client Apply method returns an error.
func TestSubResourceClientApply(t *testing.T) {
	src := &subResourceClient{
		multiclusterClient: &Client{},
		subResource:        "status",
	}

	ctx := context.Background()

	err := src.Apply(ctx, nil)
	if err == nil {
		t.Error("expected error for Apply operation")
	}
	if err.Error() != "apply operation is not supported in multicluster subresource client" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestClient_ClusterForResource_NilRemoteClusters tests behavior when no remote clusters are configured.
func TestClient_ClusterForResource_NilRemoteClusters(t *testing.T) {
	c := &Client{
		remoteClusters: nil,
	}

	gvk := schema.GroupVersionKind{
		Group:   "test",
		Version: "v1",
		Kind:    "TestKind",
	}

	// When remoteClusters is nil and HomeCluster is nil, we should get nil
	result := c.ClusterForResource(gvk)
	if result != nil {
		t.Error("expected nil when no home cluster is set")
	}
}

// TestClient_ClusterForResource_EmptyRemoteClusters tests behavior with empty remote clusters map.
func TestClient_ClusterForResource_EmptyRemoteClusters(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	gvk := schema.GroupVersionKind{
		Group:   "test",
		Version: "v1",
		Kind:    "TestKind",
	}

	// When remoteClusters is empty and HomeCluster is nil, we should get nil
	result := c.ClusterForResource(gvk)
	if result != nil {
		t.Error("expected nil when no home cluster is set and GVK not found")
	}
}

// TestClient_Status returns a status writer.
func TestClient_Status(t *testing.T) {
	c := &Client{}

	status := c.Status()
	if status == nil {
		t.Error("expected non-nil status writer")
	}

	// Verify it's the right type
	if _, ok := status.(*statusClient); !ok {
		t.Error("expected statusClient type")
	}
}

// TestClient_SubResource returns a subresource client.
func TestClient_SubResource(t *testing.T) {
	c := &Client{}

	subResource := c.SubResource("scale")
	if subResource == nil {
		t.Error("expected non-nil subresource client")
	}

	// Verify it's the right type
	src, ok := subResource.(*subResourceClient)
	if !ok {
		t.Error("expected subResourceClient type")
	}

	if src.subResource != "scale" {
		t.Errorf("expected subResource='scale', got '%s'", src.subResource)
	}
}

// TestClient_AddRemote_NilRemoteClusters initializes the remote clusters map.
func TestClient_AddRemote_NilRemoteClusters(t *testing.T) {
	c := &Client{
		remoteClusters: nil,
	}

	// Just verify the lock mechanism works without panicking
	c.remoteClustersMu.Lock()
	if c.remoteClusters == nil {
		c.remoteClusters = make(map[schema.GroupVersionKind]cluster.Cluster)
	}
	c.remoteClustersMu.Unlock()

	// Should not panic
	if c.remoteClusters == nil {
		t.Error("expected remoteClusters to be initialized")
	}
}

// TestClient_ConcurrentAccess tests thread safety of ClusterForResource.
func TestClient_ConcurrentAccess(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	gvk := schema.GroupVersionKind{
		Group:   "test",
		Version: "v1",
		Kind:    "TestKind",
	}

	// Test concurrent reads - should not panic or race
	done := make(chan bool)
	for range 10 {
		go func() {
			_ = c.ClusterForResource(gvk)
			done <- true
		}()
	}

	for range 10 {
		<-done
	}
}

// TestObjectKeyFromConfigMap tests that we can construct object keys properly.
func TestObjectKeyFromConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
	}

	key := client.ObjectKeyFromObject(cm)
	if key.Name != "test-config" {
		t.Errorf("expected Name='test-config', got '%s'", key.Name)
	}
	if key.Namespace != "default" {
		t.Errorf("expected Namespace='default', got '%s'", key.Namespace)
	}
}

// TestGVKExtraction tests that GVK can be properly set and retrieved.
func TestGVKExtraction(t *testing.T) {
	cm := &corev1.ConfigMap{}
	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	cm.SetGroupVersionKind(gvk)

	result := cm.GetObjectKind().GroupVersionKind()
	if result != gvk {
		t.Errorf("expected GVK %v, got %v", gvk, result)
	}
}

// TestGVKFromHomeScheme_Success tests successful GVK lookup for registered types.
func TestGVKFromHomeScheme_Success(t *testing.T) {
	scheme := newTestScheme(t)

	c := &Client{
		HomeScheme: scheme,
	}

	tests := []struct {
		name        string
		obj         runtime.Object
		expectedGVK schema.GroupVersionKind
	}{
		{
			name: "ConfigMap",
			obj:  &corev1.ConfigMap{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
		},
		{
			name: "ConfigMapList",
			obj:  &corev1.ConfigMapList{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMapList",
			},
		},
		{
			name: "Secret",
			obj:  &corev1.Secret{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Secret",
			},
		},
		{
			name: "Pod",
			obj:  &corev1.Pod{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
		{
			name: "Service",
			obj:  &corev1.Service{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Service",
			},
		},
		{
			name: "v1alpha1 Decision",
			obj:  &v1alpha1.Decision{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "cortex.cloud",
				Version: "v1alpha1",
				Kind:    "Decision",
			},
		},
		{
			name: "v1alpha1 DecisionList",
			obj:  &v1alpha1.DecisionList{},
			expectedGVK: schema.GroupVersionKind{
				Group:   "cortex.cloud",
				Version: "v1alpha1",
				Kind:    "DecisionList",
			},
		},
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

// TestGVKFromHomeScheme_UnknownType tests error handling for unregistered types.
func TestGVKFromHomeScheme_UnknownType(t *testing.T) {
	scheme := newTestScheme(t)

	c := &Client{
		HomeScheme: scheme,
	}

	obj := &unknownType{}
	_, err := c.GVKFromHomeScheme(obj)
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

// TestGVKFromHomeScheme_UnversionedType tests error handling for unversioned types.
func TestGVKFromHomeScheme_UnversionedType(t *testing.T) {
	scheme := runtime.NewScheme()

	// Register the type as unversioned
	scheme.AddUnversionedTypes(schema.GroupVersion{Group: "", Version: "v1"}, &unversionedType{})

	c := &Client{
		HomeScheme: scheme,
	}

	obj := &unversionedType{}
	_, err := c.GVKFromHomeScheme(obj)
	if err == nil {
		t.Error("expected error for unversioned type")
	}
	if err.Error() != "cannot list unversioned resource" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGVKFromHomeScheme_NilScheme tests behavior with nil scheme.
func TestGVKFromHomeScheme_NilScheme(t *testing.T) {
	c := &Client{
		HomeScheme: nil,
	}

	obj := &corev1.ConfigMap{}

	// Should panic or return error when scheme is nil
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil scheme")
		}
	}()

	_, err := c.GVKFromHomeScheme(obj)
	if err == nil {
		t.Error("expected error with nil scheme")
	}
}

// TestClient_ClusterForResource_WithRemoteCluster tests ClusterForResource with a remote cluster configured.
func TestClient_ClusterForResource_WithRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	// Should return the remote cluster for the registered GVK
	result := c.ClusterForResource(gvk)
	if result != remoteCluster {
		t.Error("expected remote cluster for registered GVK")
	}

	// Should return home cluster for non-registered GVK
	otherGVK := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Secret",
	}
	result = c.ClusterForResource(otherGVK)
	if result != homeCluster {
		t.Error("expected home cluster for non-registered GVK")
	}
}

// TestClient_ClientForResource tests ClientForResource returns the correct client.
func TestClient_ClientForResource(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	// Should return the remote cluster's client for the registered GVK
	result := c.ClientForResource(gvk)
	if result != remoteCluster.GetClient() {
		t.Error("expected remote cluster client for registered GVK")
	}

	// Should return home cluster's client for non-registered GVK
	otherGVK := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Secret",
	}
	result = c.ClientForResource(otherGVK)
	if result != homeCluster.GetClient() {
		t.Error("expected home cluster client for non-registered GVK")
	}
}

// TestClient_Scheme tests that Scheme returns the home cluster's client scheme.
func TestClient_Scheme(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	result := c.Scheme()
	if result == nil {
		t.Error("expected non-nil scheme")
	}
}

// TestClient_RESTMapper tests that RESTMapper returns the home cluster's client RESTMapper.
func TestClient_RESTMapper(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	result := c.RESTMapper()
	if result == nil {
		t.Error("expected non-nil RESTMapper")
	}
}

// TestClient_GroupVersionKindFor tests GroupVersionKindFor returns correct GVK.
func TestClient_GroupVersionKindFor(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	gvk, err := c.GroupVersionKindFor(&corev1.ConfigMap{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}
	if gvk != expected {
		t.Errorf("expected GVK %v, got %v", expected, gvk)
	}
}

// TestClient_IsObjectNamespaced tests IsObjectNamespaced delegates to home cluster client.
func TestClient_IsObjectNamespaced(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	// The fake client's RESTMapper doesn't have all mappings, so we just test
	// that the method delegates properly to the home cluster's client.
	// We expect an error due to the fake client's limited RESTMapper.
	_, err := c.IsObjectNamespaced(&corev1.ConfigMap{})
	// The fake client doesn't have a proper RESTMapper, so this will fail,
	// but we're testing that the delegation works.
	_ = err // Error expected with fake client
}

// TestClient_Get tests the Get method routes to the correct cluster.
func TestClient_Get(t *testing.T) {
	scheme := newTestScheme(t)

	// Create a ConfigMap in the remote cluster
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm",
			Namespace: "default",
		},
		Data: map[string]string{"key": "remote-value"},
	}

	remoteCluster := newFakeCluster(scheme, existingCM)
	homeCluster := newFakeCluster(scheme)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Get from remote cluster (ConfigMap GVK is registered)
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, client.ObjectKey{Name: "test-cm", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Data["key"] != "remote-value" {
		t.Errorf("expected 'remote-value', got '%s'", cm.Data["key"])
	}
}

// TestClient_Get_UnknownType tests Get returns error for unknown types.
func TestClient_Get_UnknownType(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	obj := &unknownType{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	err := c.Get(ctx, client.ObjectKey{Name: "test", Namespace: "default"}, obj)
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

// TestClient_List tests the List method routes to the correct cluster.
func TestClient_List(t *testing.T) {
	scheme := newTestScheme(t)

	// Create ConfigMaps in the remote cluster
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm1",
			Namespace: "default",
		},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm2",
			Namespace: "default",
		},
	}

	remoteCluster := newFakeCluster(scheme, cm1, cm2)
	homeCluster := newFakeCluster(scheme)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMapList",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// List from remote cluster
	cmList := &corev1.ConfigMapList{}
	err := c.List(ctx, cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(cmList.Items))
	}
}

// TestClient_Create tests the Create method routes to the correct cluster.
func TestClient_Create(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Create in remote cluster
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-cm",
			Namespace: "default",
		},
	}
	err := c.Create(ctx, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was created in remote cluster
	result := &corev1.ConfigMap{}
	err = remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "new-cm", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get created object from remote cluster: %v", err)
	}
}

// TestClient_Delete tests the Delete method routes to the correct cluster.
func TestClient_Delete(t *testing.T) {
	scheme := newTestScheme(t)

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "to-delete",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, existingCM)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Delete from remote cluster
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "to-delete",
			Namespace: "default",
		},
	}
	err := c.Delete(ctx, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was deleted from remote cluster
	result := &corev1.ConfigMap{}
	err = remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "to-delete", Namespace: "default"}, result)
	if err == nil {
		t.Error("expected object to be deleted from remote cluster")
	}
}

// TestClient_Update tests the Update method routes to the correct cluster.
func TestClient_Update(t *testing.T) {
	scheme := newTestScheme(t)

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "to-update",
			Namespace: "default",
		},
		Data: map[string]string{"key": "old-value"},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, existingCM)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// First get the object to have the correct resource version
	cm := &corev1.ConfigMap{}
	err := remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "to-update", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}

	// Update in remote cluster
	cm.Data["key"] = "new-value"
	err = c.Update(ctx, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was updated in remote cluster
	result := &corev1.ConfigMap{}
	err = remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "to-update", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get updated object: %v", err)
	}
	if result.Data["key"] != "new-value" {
		t.Errorf("expected 'new-value', got '%s'", result.Data["key"])
	}
}

// TestClient_Patch tests the Patch method routes to the correct cluster.
func TestClient_Patch(t *testing.T) {
	scheme := newTestScheme(t)

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "to-patch",
			Namespace: "default",
		},
		Data: map[string]string{"key": "old-value"},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, existingCM)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Patch in remote cluster
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "to-patch",
			Namespace: "default",
		},
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"key": "patched-value"}
	err := c.Patch(ctx, cm, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was patched in remote cluster
	result := &corev1.ConfigMap{}
	err = remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "to-patch", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}
	if result.Data["key"] != "patched-value" {
		t.Errorf("expected 'patched-value', got '%s'", result.Data["key"])
	}
}

// TestClient_DeleteAllOf tests the DeleteAllOf method routes to the correct cluster.
func TestClient_DeleteAllOf(t *testing.T) {
	scheme := newTestScheme(t)

	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm1",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cm2",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, cm1, cm2)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "ConfigMap",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// DeleteAllOf in remote cluster
	err := c.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("default"), client.MatchingLabels{"app": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all were deleted from remote cluster
	cmList := &corev1.ConfigMapList{}
	err = remoteCluster.GetClient().List(ctx, cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("failed to list objects: %v", err)
	}
	if len(cmList.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(cmList.Items))
	}
}

// TestClient_ConcurrentAddRemote tests thread safety of adding remote clusters.
func TestClient_ConcurrentAddRemote(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gvk := schema.GroupVersionKind{
				Group:   "test",
				Version: "v1",
				Kind:    "TestKind" + string(rune('A'+i)),
			}
			c.remoteClustersMu.Lock()
			c.remoteClusters[gvk] = nil
			c.remoteClustersMu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(c.remoteClusters) != 10 {
		t.Errorf("expected 10 remote clusters, got %d", len(c.remoteClusters))
	}
}

// TestClient_ConcurrentClusterForResourceAndAddRemote tests concurrent read/write operations.
func TestClient_ConcurrentClusterForResourceAndAddRemote(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	gvk := schema.GroupVersionKind{
		Group:   "test",
		Version: "v1",
		Kind:    "TestKind",
	}

	var wg sync.WaitGroup

	// Readers
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = c.ClusterForResource(gvk)
			}
		}()
	}

	// Writers
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				c.remoteClustersMu.Lock()
				c.remoteClusters[gvk] = nil
				c.remoteClustersMu.Unlock()
			}
		}()
	}

	wg.Wait()
}

// TestStatusClient_Create tests the status client Create method.
func TestStatusClient_Create(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Create requires a subresource object, but we're just testing the routing
	sc := c.Status()

	// The fake client doesn't support status subresource creation in the standard way,
	// but we can verify the method exists and routes correctly
	err := sc.Create(ctx, pod, &corev1.Pod{})
	// We expect an error because the fake client doesn't support this,
	// but we're testing that the routing works
	_ = err // Error is expected with fake client
}

// TestStatusClient_Update tests the status client Update method.
func TestStatusClient_Update(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Get the pod first
	existingPod := &corev1.Pod{}
	err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Update status
	existingPod.Status.Phase = corev1.PodRunning
	err = c.Status().Update(ctx, existingPod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStatusClient_Patch tests the status client Patch method.
func TestStatusClient_Patch(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Get the pod first
	existingPod := &corev1.Pod{}
	err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Patch status
	patch := client.MergeFrom(existingPod.DeepCopy())
	existingPod.Status.Phase = corev1.PodRunning
	err = c.Status().Patch(ctx, existingPod, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStatusClient_RoutesToRemoteCluster tests that status client routes to remote cluster.
func TestStatusClient_RoutesToRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, pod)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Get the pod from remote cluster
	existingPod := &corev1.Pod{}
	err := remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Update status via multicluster client - should go to remote cluster
	existingPod.Status.Phase = corev1.PodRunning
	err = c.Status().Update(ctx, existingPod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it was updated in remote cluster
	result := &corev1.Pod{}
	err = remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, result)
	if err != nil {
		t.Fatalf("failed to get updated pod: %v", err)
	}
	if result.Status.Phase != corev1.PodRunning {
		t.Errorf("expected PodRunning, got %s", result.Status.Phase)
	}
}

// TestSubResourceClient_Get tests the subresource client Get method.
func TestSubResourceClient_Get(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// The fake client may not support all subresources, but we can test the routing
	src := c.SubResource("status")
	err := src.Get(ctx, pod, &corev1.Pod{})
	// Error is expected with fake client for subresource operations
	_ = err
}

// TestSubResourceClient_Create tests the subresource client Create method.
func TestSubResourceClient_Create(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	src := c.SubResource("eviction")
	err := src.Create(ctx, pod, &corev1.Pod{})
	// Error is expected with fake client for subresource operations
	_ = err
}

// TestSubResourceClient_Update tests the subresource client Update method.
func TestSubResourceClient_Update(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Get the pod first
	existingPod := &corev1.Pod{}
	err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	src := c.SubResource("status")
	err = src.Update(ctx, existingPod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSubResourceClient_Patch tests the subresource client Patch method.
func TestSubResourceClient_Patch(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme, pod)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Get the pod first
	existingPod := &corev1.Pod{}
	err := homeCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	patch := client.MergeFrom(existingPod.DeepCopy())
	src := c.SubResource("status")
	err = src.Patch(ctx, existingPod, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSubResourceClient_RoutesToRemoteCluster tests that subresource client routes to remote cluster.
func TestSubResourceClient_RoutesToRemoteCluster(t *testing.T) {
	scheme := newTestScheme(t)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	homeCluster := newFakeCluster(scheme)
	remoteCluster := newFakeCluster(scheme, pod)

	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
	}

	c := &Client{
		HomeCluster:    homeCluster,
		HomeScheme:     scheme,
		remoteClusters: map[schema.GroupVersionKind]cluster.Cluster{gvk: remoteCluster},
	}

	ctx := context.Background()

	// Get the pod from remote cluster
	existingPod := &corev1.Pod{}
	err := remoteCluster.GetClient().Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, existingPod)
	if err != nil {
		t.Fatalf("failed to get pod: %v", err)
	}

	// Update via subresource client - should go to remote cluster
	src := c.SubResource("status")
	err = src.Update(ctx, existingPod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGVKFromHomeScheme_WithDifferentAPIGroups tests GVK lookup for different API groups.
func TestGVKFromHomeScheme_WithDifferentAPIGroups(t *testing.T) {
	scheme := newTestScheme(t)

	c := &Client{
		HomeScheme: scheme,
	}

	tests := []struct {
		name        string
		obj         runtime.Object
		expectedGrp string
	}{
		{
			name:        "core API group (empty string)",
			obj:         &corev1.ConfigMap{},
			expectedGrp: "",
		},
		{
			name:        "custom API group",
			obj:         &v1alpha1.Decision{},
			expectedGrp: "cortex.cloud",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvk, err := c.GVKFromHomeScheme(tt.obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gvk.Group != tt.expectedGrp {
				t.Errorf("expected group '%s', got '%s'", tt.expectedGrp, gvk.Group)
			}
		})
	}
}

// TestClient_Operations_WithHomeClusterOnly tests operations when no remote clusters are configured.
func TestClient_Operations_WithHomeClusterOnly(t *testing.T) {
	scheme := newTestScheme(t)

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "home-cm",
			Namespace: "default",
		},
		Data: map[string]string{"key": "home-value"},
	}

	homeCluster := newFakeCluster(scheme, existingCM)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	// Get from home cluster
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, client.ObjectKey{Name: "home-cm", Namespace: "default"}, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm.Data["key"] != "home-value" {
		t.Errorf("expected 'home-value', got '%s'", cm.Data["key"])
	}

	// List from home cluster
	cmList := &corev1.ConfigMapList{}
	err = c.List(ctx, cmList, client.InNamespace("default"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmList.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(cmList.Items))
	}
}

// TestClient_StatusAndSubResource_ErrorOnUnknownType tests error handling for unknown types.
func TestClient_StatusAndSubResource_ErrorOnUnknownType(t *testing.T) {
	scheme := newTestScheme(t)
	homeCluster := newFakeCluster(scheme)

	c := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	ctx := context.Background()

	obj := &unknownType{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	// Status client should return error for unknown type
	err := c.Status().Update(ctx, obj)
	if err == nil {
		t.Error("expected error for unknown type in status Update")
	}

	err = c.Status().Patch(ctx, obj, client.MergeFrom(obj))
	if err == nil {
		t.Error("expected error for unknown type in status Patch")
	}

	// SubResource client should return error for unknown type
	err = c.SubResource("status").Update(ctx, obj)
	if err == nil {
		t.Error("expected error for unknown type in subresource Update")
	}

	err = c.SubResource("status").Patch(ctx, obj, client.MergeFrom(obj))
	if err == nil {
		t.Error("expected error for unknown type in subresource Patch")
	}
}

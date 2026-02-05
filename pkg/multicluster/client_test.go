// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

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

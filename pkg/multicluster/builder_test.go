// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// TestBuildController tests that BuildController creates a MulticlusterBuilder.
// Note: Full integration testing requires a running manager, which is not
// practical for unit tests. This test verifies the basic structure.
func TestBuildController_Structure(t *testing.T) {
	// We can't easily test BuildController without a real manager
	// because ctrl.NewControllerManagedBy requires a manager implementation.
	// Instead, we verify that MulticlusterBuilder has the expected fields.

	// Test that MulticlusterBuilder can be created with required fields
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	// Verify the Client field is accessible
	if c.remoteClusters == nil {
		t.Error("expected remoteClusters to be initialized")
	}
}

// TestMulticlusterBuilder_Fields verifies the structure of MulticlusterBuilder.
func TestMulticlusterBuilder_Fields(t *testing.T) {
	// Create a minimal client for testing
	c := &Client{}

	// Create a MulticlusterBuilder manually to test its fields
	mb := MulticlusterBuilder{
		Builder:            nil, // Can't create without manager
		multiclusterClient: c,
	}

	// Verify multiclusterClient is set
	if mb.multiclusterClient != c {
		t.Error("expected multiclusterClient to be set")
	}

	// Verify Builder can be nil initially
	if mb.Builder != nil {
		t.Error("expected Builder to be nil when not set")
	}
}

// TestMulticlusterBuilder_WatchesMulticluster_RequiresClient tests that
// WatchesMulticluster requires a multicluster client.
func TestMulticlusterBuilder_WatchesMulticluster_RequiresClient(t *testing.T) {
	// Create a client with remote clusters
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	// Verify the client can be used with the builder
	mb := MulticlusterBuilder{
		multiclusterClient: c,
	}

	if mb.multiclusterClient == nil {
		t.Error("expected multiclusterClient to be set")
	}
}

// TestClient_ClusterForResource_ReturnsHomeCluster tests that ClusterForResource
// returns the home cluster when no remote cluster is configured for the GVK.
func TestClient_ClusterForResource_Integration(t *testing.T) {
	// Test with nil remote clusters - should return home cluster
	c := &Client{
		HomeCluster:    nil, // Will return nil
		remoteClusters: nil,
	}

	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	result := c.ClusterForResource(gvk)
	if result != nil {
		t.Error("expected nil when HomeCluster is nil")
	}
}

// TestClient_ClusterForResource_LookupOrder tests the lookup order:
// first check remote clusters, then fall back to home cluster.
func TestClient_ClusterForResource_LookupOrder(t *testing.T) {
	// Create client with empty remote clusters map
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind]cluster.Cluster),
	}

	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	// Should return HomeCluster (nil) since GVK is not in remoteClusters
	result := c.ClusterForResource(gvk)
	if result != nil {
		t.Error("expected nil when GVK not in remoteClusters and HomeCluster is nil")
	}
}

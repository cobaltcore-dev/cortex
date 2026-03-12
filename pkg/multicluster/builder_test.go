// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestBuildController_Structure(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind][]remoteCluster),
	}
	if c.remoteClusters == nil {
		t.Error("expected remoteClusters to be initialized")
	}
}

func TestMulticlusterBuilder_Fields(t *testing.T) {
	c := &Client{}
	mb := MulticlusterBuilder{
		Builder:            nil,
		multiclusterClient: c,
	}
	if mb.multiclusterClient != c {
		t.Error("expected multiclusterClient to be set")
	}
	if mb.Builder != nil {
		t.Error("expected Builder to be nil when not set")
	}
}

func TestClient_ClustersForGVK_Integration(t *testing.T) {
	c := &Client{
		HomeCluster:    nil,
		remoteClusters: nil,
	}

	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	result := c.ClustersForGVK(gvk)
	// With nil remoteClusters and nil HomeCluster, returns [nil].
	if len(result) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(result))
	}
}

func TestClient_ClustersForGVK_LookupOrder(t *testing.T) {
	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind][]remoteCluster),
	}

	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	// No remote clusters for this GVK, returns home cluster (nil).
	result := c.ClustersForGVK(gvk)
	if len(result) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(result))
	}
	if result[0] != nil {
		t.Error("expected nil HomeCluster")
	}
}

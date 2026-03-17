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

func TestClient_ClustersForGVK_UnknownGVKReturnsError(t *testing.T) {
	c := &Client{
		HomeCluster:    nil,
		remoteClusters: nil,
	}

	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	_, err := c.ClustersForGVK(gvk)
	if err == nil {
		t.Error("expected error for unknown GVK")
	}
}

func TestClient_ClustersForGVK_HomeGVKNilHomeClusterReturnsEmpty(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	c := &Client{
		remoteClusters: make(map[schema.GroupVersionKind][]remoteCluster),
		homeGVKs:       map[schema.GroupVersionKind]bool{gvk: true},
	}

	// GVK is in homeGVKs but HomeCluster is nil, so no clusters are returned.
	result, err := c.ClustersForGVK(gvk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 clusters when HomeCluster is nil, got %d", len(result))
	}
}

func TestClient_ClustersForGVK_HomeGVKReturnsHomeCluster(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	home := &fakeCluster{}
	c := &Client{
		HomeCluster:    home,
		remoteClusters: make(map[schema.GroupVersionKind][]remoteCluster),
		homeGVKs:       map[schema.GroupVersionKind]bool{gvk: true},
	}

	result, err := c.ClustersForGVK(gvk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(result))
	}
	if result[0] != home {
		t.Error("expected HomeCluster to be returned")
	}
}

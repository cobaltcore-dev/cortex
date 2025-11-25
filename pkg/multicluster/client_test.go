// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type testResource struct {
	client.Object
	uri string
}

func (t *testResource) URI() string {
	return t.uri
}

type testNonResource struct {
	client.Object
}

func TestResource_Interface(t *testing.T) {
	// Test that our test resource implements the Resource interface
	var _ Resource = &testResource{}

	resource := &testResource{uri: "test-uri"}
	if resource.URI() != "test-uri" {
		t.Errorf("Expected URI 'test-uri', got '%s'", resource.URI())
	}
}

func TestClient_ResourceTypeDetection(t *testing.T) {
	tests := []struct {
		name        string
		object      client.Object
		isResource  bool
		expectedURI string
	}{
		{
			name:        "resource with URI",
			object:      &testResource{uri: "test-uri"},
			isResource:  true,
			expectedURI: "test-uri",
		},
		{
			name:        "resource with empty URI",
			object:      &testResource{uri: ""},
			isResource:  true,
			expectedURI: "",
		},
		{
			name:        "non-resource object",
			object:      &testNonResource{},
			isResource:  false,
			expectedURI: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, ok := tt.object.(Resource)
			if ok != tt.isResource {
				t.Errorf("Expected isResource=%v, got %v", tt.isResource, ok)
			}

			if ok {
				uri := resource.URI()
				if uri != tt.expectedURI {
					t.Errorf("Expected URI '%s', got '%s'", tt.expectedURI, uri)
				}
			}
		})
	}
}

func TestClient_ClusterForResource_NilSafety(t *testing.T) {
	// Test that ClusterForResource handles nil home cluster gracefully
	client := &Client{}

	cluster := client.ClusterForResource("any-uri")
	if cluster != nil {
		t.Error("Expected nil cluster when home cluster is nil")
	}
}

func TestClient_ClusterForResource_EmptyRemoteClusters(t *testing.T) {
	// Test behavior with nil remote clusters map
	client := &Client{
		remoteClusters: nil,
	}

	cluster := client.ClusterForResource("any-uri")
	if cluster != nil {
		t.Error("Expected nil cluster when both home and remote clusters are nil")
	}
}

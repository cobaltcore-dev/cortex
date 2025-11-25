// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockResource struct {
	client.Object
	uri string
}

func (m *mockResource) URI() string {
	return m.uri
}

type mockNonResource struct {
	client.Object
}

func TestBuilder_ResourceInterface(t *testing.T) {
	// Test that our mock resource implements the Resource interface
	var _ Resource = &mockResource{}

	resource := &mockResource{uri: "test-uri"}
	if resource.URI() != "test-uri" {
		t.Errorf("Expected URI 'test-uri', got '%s'", resource.URI())
	}
}

func TestResource_TypeAssertion(t *testing.T) {
	tests := []struct {
		name       string
		object     client.Object
		isResource bool
	}{
		{
			name:       "resource object",
			object:     &mockResource{uri: "test-uri"},
			isResource: true,
		},
		{
			name:       "non-resource object",
			object:     &mockNonResource{},
			isResource: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := tt.object.(Resource)
			if ok != tt.isResource {
				t.Errorf("Expected isResource=%v, got %v", tt.isResource, ok)
			}
		})
	}
}

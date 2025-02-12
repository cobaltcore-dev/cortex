// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
)

func TestListResourceProviders(t *testing.T) {
	mockResponse := `{
        "resource_providers": [
            {
                "uuid": "provider1",
                "name": "Provider 1",
                "parent_provider_uuid": "parent1",
                "root_provider_uuid": "root1"
            },
            {
                "uuid": "provider2",
                "name": "Provider 2",
                "parent_provider_uuid": "parent2",
                "root_provider_uuid": "root2"
            }
        ]
    }`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(mockResponse)); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}))
	defer server.Close()

	api := &placementAPI{
		conf: conf.SyncOpenStackConfig{
			PlacementURL: server.URL,
		},
		client:  server.Client(),
		monitor: sync.Monitor{},
	}

	auth := KeystoneAuth{
		token: "test-token",
	}
	providers, err := api.ListResourceProviders(auth)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}

	expectedUUIDs := []string{"provider1", "provider2"}
	for i, provider := range providers {
		if provider.UUID != expectedUUIDs[i] {
			t.Errorf("expected UUID %s, got %s", expectedUUIDs[i], provider.UUID)
		}
	}
}

func TestResolveTraits(t *testing.T) {
	mockResponse := `{
        "traits": ["trait1", "trait2"]
    }`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(mockResponse)); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}))
	defer server.Close()

	api := &placementAPI{
		conf: conf.SyncOpenStackConfig{
			PlacementURL: server.URL,
		},
		client:  server.Client(),
		monitor: sync.Monitor{},
	}

	auth := KeystoneAuth{
		token: "test-token",
	}
	provider := ResourceProvider{UUID: "provider1"}
	traits, err := api.ResolveTraits(auth, provider)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(traits) != 2 {
		t.Errorf("expected 2 traits, got %d", len(traits))
	}

	expectedTraits := []string{"trait1", "trait2"}
	for i, trait := range traits {
		if trait.Name != expectedTraits[i] {
			t.Errorf("expected trait %s, got %s", expectedTraits[i], trait.Name)
		}
	}
}

func TestResolveAggregates(t *testing.T) {
	mockResponse := `{
        "aggregates": ["aggregate1", "aggregate2"]
    }`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(mockResponse)); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}))
	defer server.Close()

	api := &placementAPI{
		conf: conf.SyncOpenStackConfig{
			PlacementURL: server.URL,
		},
		client:  server.Client(),
		monitor: sync.Monitor{},
	}

	auth := KeystoneAuth{
		token: "test-token",
	}
	provider := ResourceProvider{UUID: "provider1"}
	aggregates, err := api.ResolveAggregates(auth, provider)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(aggregates) != 2 {
		t.Errorf("expected 2 aggregates, got %d", len(aggregates))
	}

	expectedAggregates := []string{"aggregate1", "aggregate2"}
	for i, aggregate := range aggregates {
		if aggregate.UUID != expectedAggregates[i] {
			t.Errorf("expected aggregate %s, got %s", expectedAggregates[i], aggregate.UUID)
		}
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"testing"
)

func TestResourceProviderTrait_GetName(t *testing.T) {
	trait := ResourceProviderTrait{}
	expected := "openstack_resource_provider_trait"
	if trait.GetName() != expected {
		t.Errorf("expected %s, got %s", expected, trait.GetName())
	}
}

func TestResourceProviderAggregate_GetName(t *testing.T) {
	aggregate := ResourceProviderAggregate{}
	expected := "openstack_resource_provider_aggregate"
	if aggregate.GetName() != expected {
		t.Errorf("expected %s, got %s", expected, aggregate.GetName())
	}
}

func TestResourceProvider_JSONMarshalling(t *testing.T) {
	provider := ResourceProvider{
		UUID:                       "provider1",
		Name:                       "Provider 1",
		ParentProviderUUID:         "parent1",
		RootProviderUUID:           "root1",
		ResourceProviderGeneration: 0,
	}

	data, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := `{"uuid":"provider1","name":"Provider 1","parent_provider_uuid":"parent1","root_provider_uuid":"root1","resource_provider_generation":0}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	var unmarshalledProvider ResourceProvider
	err = json.Unmarshal(data, &unmarshalledProvider)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if unmarshalledProvider != provider {
		t.Errorf("expected %v, got %v", provider, unmarshalledProvider)
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"testing"
)

func TestResourceProvider_TableName(t *testing.T) {
	rp := ResourceProvider{}
	expected := "openstack_resource_providers"
	if rp.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, rp.TableName())
	}
}

func TestTrait_TableName(t *testing.T) {
	trait := Trait{}
	expected := "openstack_resource_provider_traits"
	if trait.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, trait.TableName())
	}
}

func TestInventoryUsage_TableName(t *testing.T) {
	inv := InventoryUsage{}
	expected := "openstack_resource_provider_inventory_usages"
	if inv.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, inv.TableName())
	}
}

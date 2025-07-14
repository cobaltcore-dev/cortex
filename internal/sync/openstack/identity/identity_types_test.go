// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"testing"
)

func TestProjects_TableName(t *testing.T) {
	project := Project{}
	expected := "openstack_projects"
	if project.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, project.TableName())
	}
}

func TestDomain_TableName(t *testing.T) {
	domain := Domain{}
	expected := "openstack_domains"
	if domain.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, domain.TableName())
	}
}

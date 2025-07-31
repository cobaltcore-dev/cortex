// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"testing"
)

func TestCommitment_TableName(t *testing.T) {
	commitment := Commitment{}
	expected := "openstack_limes_commitments"
	if commitment.TableName() != expected {
		t.Errorf("expected %s, got %s", expected, commitment.TableName())
	}
}

func TestCommitment_Indexes(t *testing.T) {
	commitment := Commitment{}
	indexes := commitment.Indexes()
	if indexes != nil {
		t.Errorf("expected nil indexes, got %v", indexes)
	}
}

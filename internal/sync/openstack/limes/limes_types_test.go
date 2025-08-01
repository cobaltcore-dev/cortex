// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"testing"
)

func TestCommitment_Indexes(t *testing.T) {
	commitment := Commitment{}
	indexes := commitment.Indexes()
	if indexes != nil {
		t.Errorf("expected nil indexes, got %v", indexes)
	}
}

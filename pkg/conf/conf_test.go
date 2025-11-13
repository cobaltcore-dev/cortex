// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"testing"
)

func TestMergeMaps(t *testing.T) {
	// Test basic merge
	dst := map[string]any{
		"a": "original",
		"b": map[string]any{"nested": "value"},
	}
	src := map[string]any{
		"a": "overridden",
		"c": "new",
	}

	mergeMaps(dst, src)

	if dst["a"] != "overridden" {
		t.Errorf("Expected 'a' to be 'overridden', got %v", dst["a"])
	}
	if dst["c"] != "new" {
		t.Errorf("Expected 'c' to be 'new', got %v", dst["c"])
	}

	// Test nested merge
	dst = map[string]any{
		"nested": map[string]any{
			"keep":     "original",
			"override": "old",
		},
	}
	src = map[string]any{
		"nested": map[string]any{
			"override": "new",
			"add":      "added",
		},
	}

	mergeMaps(dst, src)

	nested := dst["nested"].(map[string]any)
	if nested["keep"] != "original" {
		t.Errorf("Expected nested 'keep' to be 'original', got %v", nested["keep"])
	}
	if nested["override"] != "new" {
		t.Errorf("Expected nested 'override' to be 'new', got %v", nested["override"])
	}
	if nested["add"] != "added" {
		t.Errorf("Expected nested 'add' to be 'added', got %v", nested["add"])
	}

	// Test nil value handling
	dst = map[string]any{"key": "value"}
	src = map[string]any{"key": nil}

	mergeMaps(dst, src)

	if dst["key"] != "value" {
		t.Errorf("Expected 'key' to remain 'value' when src is nil, got %v", dst["key"])
	}
}

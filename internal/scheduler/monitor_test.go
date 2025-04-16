// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import "testing"

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{
			name:     "Identical slices",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "host2", "host3"},
			expected: 0,
		},
		{
			name:     "Completely different slices",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host4", "host5", "host6"},
			expected: 3,
		},
		{
			name:     "One insertion",
			a:        []string{"host1", "host2"},
			b:        []string{"host1", "host2", "host3"},
			expected: 1,
		},
		{
			name:     "One deletion",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "host2"},
			expected: 1,
		},
		{
			name:     "One substitution",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "hostX", "host3"},
			expected: 1,
		},
		{
			name:     "Empty slices",
			a:        []string{},
			b:        []string{},
			expected: 0,
		},
		{
			name:     "One empty slice",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{},
			expected: 3,
		},
		{
			name:     "Reordering",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host3", "host2", "host1"},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := levenshteinDistance(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("levenshteinDistance(%v, %v) = %d; want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import "testing"

func TestLimesUnitToResource(t *testing.T) {
	tests := []struct {
		name        string
		val         uint64
		unit        string
		expected    string
		shouldError bool
	}{
		{
			name:     "no unit",
			val:      100,
			unit:     "",
			expected: "100",
		},
		{
			name:     "bytes",
			val:      1024,
			unit:     "B",
			expected: "1Ki",
		},
		{
			name:     "KiB",
			val:      1,
			unit:     "KiB",
			expected: "1Ki",
		},
		{
			name:     "MiB",
			val:      1,
			unit:     "MiB",
			expected: "1Mi",
		},
		{
			name:     "GiB",
			val:      1,
			unit:     "GiB",
			expected: "1Gi",
		},
		{
			name:     "TiB",
			val:      1,
			unit:     "TiB",
			expected: "1Ti",
		},
		{
			name:        "unsupported unit",
			val:         100,
			unit:        "PiB",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commitment := &Commitment{
				Amount: tt.val,
				Unit:   tt.unit,
			}
			result, err := commitment.ParseResource()

			if tt.shouldError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result.String())
			}
		})
	}
}

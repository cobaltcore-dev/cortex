// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"testing"
)

func TestFilterHasEnoughCapacityOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        FilterHasEnoughCapacityOpts
		expectError bool
	}{
		{
			name: "valid options with lock reserved true",
			opts: FilterHasEnoughCapacityOpts{
				LockReserved: true,
			},
			expectError: false,
		},
		{
			name: "valid options with lock reserved false",
			opts: FilterHasEnoughCapacityOpts{
				LockReserved: false,
			},
			expectError: false,
		},
		{
			name:        "valid options with default values",
			opts:        FilterHasEnoughCapacityOpts{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

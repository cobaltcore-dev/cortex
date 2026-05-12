// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduling

import "testing"

func TestOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"zero value is valid", Options{}, false},
		{"read-only run, skipping history and inflight", Options{ReadOnly: true, SkipHistory: true, SkipInflight: true}, false},
		{"ReadOnly without SkipHistory is invalid", Options{ReadOnly: true}, true},
		{"ReadOnly without SkipInflight is invalid", Options{ReadOnly: true, SkipHistory: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

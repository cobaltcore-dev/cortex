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
		{"write run, history recorded by default", Options{}, false},
		{"write run with inflight", Options{CreateInflight: true}, false},
		{"read-only run, skipping history", Options{ReadOnly: true, SkipHistory: true}, false},
		{"ReadOnly without SkipHistory is invalid", Options{ReadOnly: true}, true},
		{"ReadOnly + CreateInflight is invalid", Options{ReadOnly: true, CreateInflight: true}, true},
		{"ReadOnly + both invalid", Options{ReadOnly: true, CreateInflight: true}, true},
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

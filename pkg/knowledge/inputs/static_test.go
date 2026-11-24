// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inputs

import (
	"errors"
	"testing"
)

type staticTarget struct {
	Names  []string `input:"names"`
	Counts []int    `input:"counts"`
	Other  string
}

func TestStatic_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		static  Static
		wantErr error
	}{
		{"all inputs", Static{"names": []string{"a"}, "counts": []int{1}}, nil},
		{"nil input", Static{"names": nil, "counts": []int{1}}, nil},
		{"missing input", Static{"names": []string{"a"}}, ErrMissing},
		{"unused input", Static{"names": []string{"a"}, "counts": []int{1}, "other": []int{}}, ErrUnused},
		{"type mismatch", Static{"names": []int{1}, "counts": []int{1}}, ErrTypeMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target staticTarget
			err := tt.static.Resolve(t.Context(), &target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if err == nil && len(target.Counts) != 1 {
				t.Errorf("expected counts to be resolved, got %v", target.Counts)
			}
		})
	}
}

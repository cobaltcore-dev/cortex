// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package expression

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEvaluator_RunBool(t *testing.T) {
	tests := []struct {
		name    string
		run     string
		env     map[string]any
		want    bool
		wantErr error
	}{
		{name: "true", run: "x > 1", env: map[string]any{"x": 2}, want: true},
		{name: "false", run: "x > 1", env: map[string]any{"x": 0}, want: false},
		{name: "compile error", run: "x >", env: map[string]any{"x": 1}, wantErr: ErrCompile},
		{name: "not a bool", run: "x + 1", env: map[string]any{"x": 1}, wantErr: ErrOutput},
	}
	var e Evaluator
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.RunBool(context.Background(), tt.name, tt.run, tt.env, DefaultTimeout, DefaultMemoryBudget)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestEvaluator_RunCachesProgram(t *testing.T) {
	var e Evaluator
	env := map[string]any{"x": 1}
	if _, err := e.Run(context.Background(), "k", "x + 1", env, DefaultTimeout, DefaultMemoryBudget); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first := e.programs["k"].program
	if _, err := e.Run(context.Background(), "k", "x + 1", env, DefaultTimeout, DefaultMemoryBudget); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.programs["k"].program != first {
		t.Fatal("expected cached program to be reused")
	}
	// Changing the expression recompiles.
	if _, err := e.Run(context.Background(), "k", "x + 2", env, DefaultTimeout, DefaultMemoryBudget); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.programs["k"].program == first {
		t.Fatal("expected program to be recompiled after expression change")
	}
}

func TestEvaluator_RunTimeout(t *testing.T) {
	var e Evaluator
	// An endless loop must be cut off by the timeout.
	_, err := e.Run(context.Background(), "loop", "reduce(1..100000000, #acc + #, 0)", map[string]any{}, time.Nanosecond, DefaultMemoryBudget)
	if !errors.Is(err, ErrDeadline) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestEvaluator_Forget(t *testing.T) {
	var e Evaluator
	if _, err := e.Run(context.Background(), "k", "1 == 1", map[string]any{}, DefaultTimeout, DefaultMemoryBudget); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e.Forget("k")
	if _, ok := e.programs["k"]; ok {
		t.Fatal("expected program to be forgotten")
	}
}

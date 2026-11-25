// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package expression provides a small, reusable wrapper around the expr-lang
// engine (https://expr-lang.org). It compiles and caches programs keyed by
// their source and input types, and runs them with a timeout and memory
// budget. It is used both by the knowledge extractor (to evaluate expression
// knowledges) and by the scheduling pipeline (to evaluate expression steps).
package expression

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

const (
	// DefaultTimeout is the default maximum duration of an evaluation.
	DefaultTimeout = time.Minute
	// DefaultMemoryBudget is the default memory budget of an evaluation,
	// counted by expr in allocated elements (e.g. list items, map entries).
	DefaultMemoryBudget uint = 1e8
)

var (
	ErrCompile  = errors.New("expression compilation failed")
	ErrRun      = errors.New("expression evaluation failed")
	ErrDeadline = errors.New("expression evaluation timed out")
	ErrOutput   = errors.New("expression output has an unexpected type")
)

// compiled holds a compiled program together with the key it was compiled for.
type compiled struct {
	// key identifies the expression source and input types the program was
	// compiled for, so a cached program is reused only when both match.
	key     string
	program *vm.Program
}

// Evaluator compiles, caches and runs expressions. The zero value is ready to
// use and safe for concurrent use.
type Evaluator struct {
	mu       sync.Mutex
	programs map[string]compiled
}

// Run compiles the expression over the given environment and evaluates it,
// returning the raw output. Compiled programs are cached per name and reused
// as long as neither the expression source nor the input types changed. The
// evaluation is bounded by the given timeout and memory budget.
func (e *Evaluator) Run(
	ctx context.Context,
	name, run string,
	env map[string]any,
	timeout time.Duration,
	memoryBudget uint,
) (any, error) {

	program, err := e.compile(name, run, env)
	if err != nil {
		return nil, err
	}
	type result struct {
		output any
		err    error
	}
	done := make(chan result, 1)
	// expr can't interrupt a running evaluation, so a timed out evaluation
	// keeps running in the background until it completes or exceeds the
	// memory budget.
	go func() {
		machine := vm.VM{MemoryBudget: memoryBudget}
		output, err := machine.Run(program, env)
		done <- result{output, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRun, r.err)
		}
		return r.output, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("%w after %s", ErrDeadline, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RunBool runs the expression and asserts that its output is a boolean.
func (e *Evaluator) RunBool(
	ctx context.Context,
	name, run string,
	env map[string]any,
	timeout time.Duration,
	memoryBudget uint,
) (bool, error) {

	output, err := e.Run(ctx, name, run, env, timeout, memoryBudget)
	if err != nil {
		return false, err
	}
	b, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("%w: expected bool, got %T", ErrOutput, output)
	}
	return b, nil
}

// compile returns the cached program for the given name, or compiles a new one
// if neither the expression source nor the input types are cached yet.
func (e *Evaluator) compile(name, run string, env map[string]any) (*vm.Program, error) {
	vars := make([]string, 0, len(env))
	for v, obj := range env {
		vars = append(vars, v+":"+reflect.TypeOf(obj).String())
	}
	slices.Sort(vars)
	key := run + "\x00" + strings.Join(vars, ",")

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.programs == nil {
		e.programs = map[string]compiled{}
	}
	if cached, ok := e.programs[name]; ok && cached.key == key {
		return cached.program, nil
	}
	program, err := expr.Compile(run, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, err)
	}
	e.programs[name] = compiled{key: key, program: program}
	return program, nil
}

// Forget drops the cached program of the given name.
func (e *Evaluator) Forget(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.programs, name)
}

// Compile compiles the expression over the given environment without caching,
// e.g. to validate it ahead of time. It returns ErrCompile on failure.
func Compile(run string, env map[string]any) error {
	if _, err := expr.Compile(run, expr.Env(env)); err != nil {
		return fmt.Errorf("%w: %w", ErrCompile, err)
	}
	return nil
}

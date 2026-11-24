// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

const (
	// Default maximum duration of an expression evaluation.
	defaultExpressionTimeout = time.Minute
	// Default memory budget of an expression evaluation, counted by expr in
	// allocated elements (e.g. list items, map entries).
	defaultExpressionMemoryBudget uint = 1e8
)

var (
	errExpressionCompile  = errors.New("expression compilation failed")
	errExpressionRun      = errors.New("expression evaluation failed")
	errExpressionOutput   = errors.New("expression output is not a list of json values")
	errExpressionDeadline = errors.New("expression evaluation timed out")
)

// Compiled expression program of a knowledge.
type compiledExpression struct {
	// Expression source and input types the program was compiled for.
	key     string
	program *vm.Program
}

// Evaluates expression knowledges, caching compiled programs by knowledge.
type expressionEvaluator struct {
	mu       sync.Mutex
	programs map[string]compiledExpression
}

// Evaluate the expression over the given inputs. The output must be a list,
// whose elements are returned as generic json values.
func (e *expressionEvaluator) evaluate(
	ctx context.Context,
	knowledgeName string,
	spec v1alpha1.KnowledgeExpressionSpec,
	env map[string]any,
	timeout time.Duration,
	memoryBudget uint,
) ([]Feature, error) {

	program, err := e.compile(knowledgeName, spec.Run, env)
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
	var output any
	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("%w: %w", errExpressionRun, r.err)
		}
		output = r.output
	case <-time.After(timeout):
		return nil, fmt.Errorf("%w after %s", errExpressionDeadline, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return decodeOutput(output)
}

// Compile the expression for the given inputs, or reuse the cached program
// if neither the expression nor the input types changed.
func (e *expressionEvaluator) compile(knowledgeName, run string, env map[string]any) (*vm.Program, error) {
	vars := make([]string, 0, len(env))
	for v, objs := range env {
		vars = append(vars, v+":"+reflect.TypeOf(objs).String())
	}
	slices.Sort(vars)
	key := run + "\x00" + strings.Join(vars, ",")

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.programs == nil {
		e.programs = map[string]compiledExpression{}
	}
	if cached, ok := e.programs[knowledgeName]; ok && cached.key == key {
		return cached.program, nil
	}
	program, err := expr.Compile(run, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errExpressionCompile, err)
	}
	e.programs[knowledgeName] = compiledExpression{key: key, program: program}
	return program, nil
}

// Forget the compiled program of a knowledge.
func (e *expressionEvaluator) forget(knowledgeName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.programs, knowledgeName)
}

// Decode the expression output, which must be a list, into generic json
// values, e.g. maps for objects.
func decodeOutput(output any) ([]Feature, error) {
	if output == nil {
		return []Feature{}, nil
	}
	if kind := reflect.ValueOf(output).Kind(); kind != reflect.Slice && kind != reflect.Array {
		return nil, fmt.Errorf("%w: got %T", errExpressionOutput, output)
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errExpressionOutput, err)
	}
	features := []Feature{}
	if err := json.Unmarshal(raw, &features); err != nil {
		return nil, fmt.Errorf("%w: %w", errExpressionOutput, err)
	}
	return features, nil
}

// EvaluateExpression evaluates an expression knowledge over the given inputs,
// keyed by their var, with the default limits of the knowledge controller.
// This can be used to test expressions against go extractors or fixtures.
func EvaluateExpression(ctx context.Context, spec v1alpha1.KnowledgeExpressionSpec, env map[string]any) ([]Feature, error) {
	var evaluator expressionEvaluator
	return evaluator.evaluate(ctx, "", spec, env, defaultExpressionTimeout, defaultExpressionMemoryBudget)
}

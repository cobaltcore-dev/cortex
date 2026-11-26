// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/expression"
)

// Re-exported for callers that match on extraction error reasons.
var (
	errExpressionCompile = expression.ErrCompile
	errExpressionOutput  = errExpressionOutputList
)

// errExpressionOutputList is returned when an expression knowledge does not
// evaluate to a list of json values.
var errExpressionOutputList = fmt.Errorf("%w: expected a list of json values", expression.ErrOutput)

// Evaluates expression knowledges, caching compiled programs by knowledge.
type expressionEvaluator struct {
	evaluator expression.Evaluator
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

	output, err := e.evaluator.Run(ctx, knowledgeName, spec.Run, env, timeout, memoryBudget)
	if err != nil {
		return nil, err
	}
	return decodeOutput(output)
}

// Forget the compiled program of a knowledge.
func (e *expressionEvaluator) forget(knowledgeName string) {
	e.evaluator.Forget(knowledgeName)
}

// Decode the expression output, which must be a list, into generic json
// values, e.g. maps for objects.
func decodeOutput(output any) ([]Feature, error) {
	if output == nil {
		return []Feature{}, nil
	}
	if kind := reflect.ValueOf(output).Kind(); kind != reflect.Slice && kind != reflect.Array {
		return nil, fmt.Errorf("%w: got %T", errExpressionOutputList, output)
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errExpressionOutputList, err)
	}
	features := []Feature{}
	if err := json.Unmarshal(raw, &features); err != nil {
		return nil, fmt.Errorf("%w: %w", errExpressionOutputList, err)
	}
	return features, nil
}

// EvaluateExpression evaluates an expression knowledge over the given inputs,
// keyed by their var, with the default limits of the knowledge controller.
// This can be used to test expressions against go extractors or fixtures.
func EvaluateExpression(ctx context.Context, spec v1alpha1.KnowledgeExpressionSpec, env map[string]any) ([]Feature, error) {
	var evaluator expressionEvaluator
	return evaluator.evaluate(ctx, "", spec, env, expression.DefaultTimeout, expression.DefaultMemoryBudget)
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"

	"github.com/go-logr/logr"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/expression"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/inputs"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the expression filter, given through the step config in the
// pipeline spec.
type FilterExpressionOpts struct {
	// Expression (https://expr-lang.org) evaluated once per host. It must
	// return a boolean: hosts for which it returns false are filtered out.
	// The host name is available as `host` and the placement request as
	// `request`. Each referenced knowledge is available as a variable named
	// by its var, holding the list of its features as generic maps.
	Run string `json:"run"`

	// Knowledges to resolve and pass to the expression, mapping the variable
	// name used in the expression to the name of the knowledge providing its
	// features. The features are decoded into generic maps accessed by their
	// json field names. Modeled as a list per var to fit the parameter schema;
	// exactly one knowledge name per var is expected.
	// +optional
	Knowledges map[string][]string `json:"knowledges,omitempty"`
}

func (o FilterExpressionOpts) Validate() error {
	if o.Run == "" {
		return errors.New("run expression must not be empty")
	}
	for v, names := range o.Knowledges {
		if len(names) != 1 {
			return fmt.Errorf("knowledge %q must reference exactly one knowledge name", v)
		}
	}
	// Dry-compile against a representative env so a broken expression fails at
	// admission instead of at scheduling time. Inputs are unknown lists here.
	env := map[string]any{"host": "", "request": compute.PlacementRequest{}}
	for v := range o.Knowledges {
		env[v] = []map[string]any{}
	}
	return expression.Compile(o.Run, env)
}

// inputs returns the input specs for the configured knowledges.
func (o FilterExpressionOpts) inputs() []v1alpha1.InputSpec {
	specs := make([]v1alpha1.InputSpec, 0, len(o.Knowledges))
	for v, names := range o.Knowledges {
		specs = append(specs, v1alpha1.InputSpec{Var: v, Knowledge: names[0]})
	}
	return specs
}

// Filter that keeps only the hosts for which a configurable expression
// evaluates to true. This allows adding new filters purely from the pipeline
// spec, e.g. on top of a dynamically added knowledge.
type FilterExpression struct {
	lib.BaseFilter[compute.PlacementRequest, FilterExpressionOpts]

	evaluator expression.Evaluator
	name      string
}

// Initialize the filter and validate that all referenced knowledges are ready.
func (s *FilterExpression) Init(ctx context.Context, client client.Client, filter v1alpha1.FilterSpec) error {
	if err := s.BaseFilter.Init(ctx, client, filter); err != nil {
		return err
	}
	s.name = filter.Name
	refs := make([]corev1.ObjectReference, 0, len(s.Options.Knowledges))
	for _, spec := range s.Options.inputs() {
		refs = append(refs, corev1.ObjectReference{Name: spec.Knowledge})
	}
	return s.CheckKnowledges(ctx, refs...)
}

// Filter out hosts for which the expression evaluates to false.
func (s *FilterExpression) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// Resolve the inputs once, then reuse them for every host. Knowledge
	// features are decoded into generic maps (map-only) to keep scheduling
	// decoupled from the extractor feature types.
	resolver := inputs.Resolver{Client: s.Client}
	resolved, err := resolver.ResolveAll(context.Background(), s.Options.inputs(), mapOnlyFeatureType)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve inputs: %w", err)
	}

	for _, host := range request.GetHosts() {
		if _, ok := result.Activations[host]; !ok {
			continue
		}
		env := map[string]any{"host": host, "request": request}
		maps.Copy(env, resolved)
		keep, err := s.evaluator.RunBool(
			context.Background(), s.name, s.Options.Run, env,
			expression.DefaultTimeout, expression.DefaultMemoryBudget,
		)
		if err != nil {
			return nil, err
		}
		if !keep {
			delete(result.Activations, host)
		}
	}
	return result, nil
}

// mapOnlyFeatureType always reports the feature type as unknown, so knowledge
// features are decoded into generic maps instead of typed structs.
func mapOnlyFeatureType(v1alpha1.KnowledgeSpec) (reflect.Type, bool) {
	return nil, false
}

func init() {
	Index["filter_expression"] = func() NovaFilter {
		return &FilterExpression{}
	}
}

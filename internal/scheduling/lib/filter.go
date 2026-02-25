// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Interface for a filter as part of the scheduling pipeline.
type Filter[RequestType FilterWeigherPipelineRequest] interface {
	FilterWeigherPipelineStep[RequestType]

	// Configure the filter and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error

	// Validate the given config parameters for this filter.
	Validate(ctx context.Context, params v1alpha1.Parameters) error
}

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseFilter[RequestType FilterWeigherPipelineRequest, Opts FilterWeigherPipelineStepOpts] struct {
	BaseFilterWeigherPipelineStep[RequestType, Opts]
}

// Init the filter with the database and options.
func (s *BaseFilter[RequestType, Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return s.BaseFilterWeigherPipelineStep.Init(ctx, client, step.Params)
}

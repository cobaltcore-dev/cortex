// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseFilter[RequestType PipelineRequest, Opts StepOpts] struct {
	BaseStep[RequestType, Opts]
}

// Init the filter with the database and options.
func (s *BaseFilter[RequestType, Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return s.BaseStep.Init(ctx, client, step.Params)
}

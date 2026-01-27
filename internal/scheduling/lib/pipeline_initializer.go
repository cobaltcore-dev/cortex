// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// Result returned by the InitPipeline interface method.
type PipelineInitResult[PipelineType any] struct {
	// The pipeline, if successfully created.
	Pipeline PipelineType

	// Errors for filters, if any, by their name.
	FilterErrors map[string]error
	// Errors for weighers, if any, by their name.
	WeigherErrors map[string]error
	// Errors for detectors, if any, by their name.
	DetectorErrors map[string]error
}

// The base pipeline controller will delegate some methods to the parent
// controller struct. The parent controller only needs to conform to this
// interface and set the delegate field accordingly.
type PipelineInitializer[PipelineType any] interface {
	// Initialize a new pipeline with the given steps.
	//
	// This method is delegated to the parent controller, when a pipeline needs
	// to be newly initialized or re-initialized to update it in the pipeline
	// map.
	InitPipeline(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[PipelineType]

	// Get the accepted pipeline type for this controller.
	//
	// This is used to filter pipelines when listing existing pipelines on
	// startup or when reacting to pipeline events.
	PipelineType() v1alpha1.PipelineType
}

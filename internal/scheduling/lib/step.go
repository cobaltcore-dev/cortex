// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"log/slog"
)

var (
	// This error is returned from the step at any time when the step should be skipped.
	ErrStepSkipped = errors.New("step skipped")
)

// Steps can be chained together to form a scheduling pipeline.
type Step[RequestType PipelineRequest] interface {
	// Run this step in the scheduling pipeline.
	//
	// The request is immutable and modifications are stored in the result.
	// This allows steps to be run in parallel (e.g. weighers) without passing
	// mutable state around.
	//
	// All hosts that should not be filtered out must be included in the returned
	// map of activations. I.e., filters implementing this interface should
	// remove activations by omitting them from the returned map.
	//
	// Filters implementing this interface should adjust activation
	// values in the returned map, including all hosts from the request.
	//
	// A traceLog is provided that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

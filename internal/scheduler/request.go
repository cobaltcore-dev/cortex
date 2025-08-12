// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import "log/slog"

type PipelineRequest interface {
	// Get the subjects that went in the pipeline.
	GetSubjects() []string
	// Get the weights for the subjects.
	GetWeights() map[string]float64

	// Get logging args to be used in the step's trace log.
	// Usually, this will be the request context including the request ID.
	GetTraceLogArgs() []slog.Attr

	// Whether the request is a sandboxed request. By default, this is false.
	//
	// Sandboxed requests can be used to notify cortex that the resource is not
	// actually being scheduled, and that sandboxed scheduler steps should be
	// executed for additional validation.
	IsSandboxed() bool

	// WithSandboxed returns a copy of the request with the sandboxed flag set.
	WithSandboxed(sandboxed bool) PipelineRequest
}

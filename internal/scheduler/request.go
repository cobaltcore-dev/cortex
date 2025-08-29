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

	// Return the selected pipeline.
	GetPipeline() string
	// Return a copy of the request with the pipeline name set.
	WithPipeline(pipeline string) PipelineRequest
}

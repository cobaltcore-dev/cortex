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

	// Whether the request is a simulated request. By default, this is false.
	//
	// Simulated requests can be used to notify cortex that the resource is not
	// actually being scheduled, but we still want to know which subjects would be
	// selected if it were to schedule the resource.
	IsSimulated() bool
}

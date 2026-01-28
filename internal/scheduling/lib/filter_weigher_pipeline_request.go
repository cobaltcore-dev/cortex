// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "log/slog"

type FilterWeigherPipelineRequest interface {
	// Get the subjects that went in the pipeline.
	GetSubjects() []string
	// This function can be used by the pipeline to obtain a mutated version
	// of the request with only the given subjects remaining. This is helpful
	// for steps that filter out subjects. Subjects not included in the map
	// are considered as filtered out, and won't be reconsidered in later steps.
	FilterSubjects(includedSubjects map[string]float64) FilterWeigherPipelineRequest
	// Get the weights for the subjects.
	GetWeights() map[string]float64
	// Get logging args to be used in the step's trace log.
	// Usually, this will be the request context including the request ID.
	GetTraceLogArgs() []slog.Attr
}

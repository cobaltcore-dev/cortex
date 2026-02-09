// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "log/slog"

type FilterWeigherPipelineRequest interface {
	// Get the hosts that went in the pipeline.
	GetHosts() []string
	// This function can be used by the pipeline to obtain a mutated version
	// of the request with only the given hosts remaining. This is helpful
	// for steps that filter out hosts. Hosts not included in the map
	// are considered as filtered out, and won't be reconsidered in later steps.
	FilterHosts(includedHosts map[string]float64) FilterWeigherPipelineRequest
	// Get the weights for the hosts.
	GetWeights() map[string]float64
	// Get logging args to be used in the step's trace log.
	// Usually, this will be the request context including the request ID.
	GetTraceLogArgs() []slog.Attr
}

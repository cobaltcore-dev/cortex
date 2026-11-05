// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

// StepResult holds the per-host activations a single pipeline step produced.
type StepResult struct {
	// StepName is the name of the filter or weigher that produced this result.
	StepName string
	// Activations maps each host to the activation the step assigned it.
	Activations map[string]float64
}

// PipelineResult is the domain-neutral output of a filter-weigher pipeline run:
// the ordered hosts, the target host, and the input/output weights plus per-step
// activations used to rank them. Domain-facing aliases (e.g.
// compute.PlacementResult) point at this type.
type PipelineResult struct {
	// RawInWeights are the input weights to the pipeline, before normalization.
	RawInWeights map[string]float64
	// NormalizedInWeights are the input weights after normalization.
	NormalizedInWeights map[string]float64
	// StepResults are the activations each pipeline step produced, in order.
	StepResults []StepResult
	// AggregatedOutWeights are the final weights after applying all weighers.
	AggregatedOutWeights map[string]float64
	// OrderedHosts is the final ordering from most to least preferred.
	OrderedHosts []string
	// TargetHost is the first element of OrderedHosts, if any.
	TargetHost *string
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

// Interface to which step options must conform.
type FilterWeigherPipelineStepOpts interface {
	// Validate the options for this step.
	Validate() error
}

// Empty step opts conforming to the StepOpts interface (validation always succeeds).
type EmptyFilterWeigherPipelineStepOpts struct{}

func (EmptyFilterWeigherPipelineStepOpts) Validate() error { return nil }

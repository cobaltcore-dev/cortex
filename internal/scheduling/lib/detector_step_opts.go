// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

// Interface to which step options must conform.
type DetectionStepOpts interface {
	// Validate the options for this step.
	Validate() error
}

// Empty step opts conforming to the StepOpts interface (validation always succeeds).
type EmptyDetectionStepOpts struct{}

func (EmptyDetectionStepOpts) Validate() error { return nil }

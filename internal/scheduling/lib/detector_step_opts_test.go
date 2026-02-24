// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"testing"
)

func TestEmptyDetectionStepOpts_Validate(t *testing.T) {
	opts := EmptyDetectionStepOpts{}
	err := opts.Validate()
	if err != nil {
		t.Errorf("expected no error from EmptyDetectionStepOpts.Validate(), got: %v", err)
	}
}

func TestDetectionStepOpts_Interface(t *testing.T) {
	// Verify EmptyDetectionStepOpts implements DetectionStepOpts interface
	var _ DetectionStepOpts = EmptyDetectionStepOpts{}
}

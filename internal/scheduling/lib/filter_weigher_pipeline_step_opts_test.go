// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"testing"
)

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

func TestEmptyFilterWeigherPipelineStepOpts_Validate(t *testing.T) {
	opts := EmptyFilterWeigherPipelineStepOpts{}
	if err := opts.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
)

var (
	// This error is returned from the step at any time when the step should be skipped.
	ErrStepSkipped = errors.New("step skipped")
)

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

var (
	// This error is returned from the step at any time when the step should be skipped.
	ErrStepSkipped = errors.New("step skipped")
)

type Step interface {
	// Get the VM ids to de-schedule.
	Run() ([]string, error)
	// Get the name of this step, used for identification in config, logs, metrics, etc.
	GetName() string
	// Configure the step with a database and options.
	Init(db db.DB, opts conf.RawOpts) error
}

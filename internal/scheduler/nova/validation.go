// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

// The config type has a long name, so we use a shorter alias here.
// The name is intentionally long to make it explicit that we disable
// validations for the scheduler step instead of enabling them.
type disabledValidations = conf.NovaSchedulerStepDisabledValidationsConfig

// Wrapper for scheduler steps that validates them before/after execution.
type StepValidator struct {
	// The wrapped step to validate.
	Step plugins.Step
	// By default, we execute all validations. However, through the config,
	// we can also disable some validations if necessary.
	DisabledValidations disabledValidations
}

// Get the name of the wrapped step.
func (s *StepValidator) GetName() string {
	return s.Step.GetName()
}

// Initialize the wrapped step with the database and options.
func (s *StepValidator) Init(db db.DB, opts conf.RawOpts) error {
	slog.Info(
		"scheduler: init validation for step", "name", s.GetName(),
		"disabled", s.DisabledValidations,
	)
	return s.Step.Init(db, opts)
}

// Validate the wrapped step with the database and options.
func validateStep[S plugins.Step](step S, disabledValidations disabledValidations) *StepValidator {
	return &StepValidator{
		Step:                step,
		DisabledValidations: disabledValidations,
	}
}

// Run the step and validate what happens.
func (s *StepValidator) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result, err := s.Step.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	// If not disabled, validate that the number of hosts stayed the same.
	if !s.DisabledValidations.SameHostNumberInOut {
		if len(result.Activations) != len(request.GetHosts()) {
			return nil, errors.New("number of hosts changed during step execution")
		}
	}
	return result, nil
}

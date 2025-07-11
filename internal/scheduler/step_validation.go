// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// The config type has a long name, so we use a shorter alias here.
// The name is intentionally long to make it explicit that we disable
// validations for the scheduler step instead of enabling them.
type disabledValidations = conf.SchedulerStepDisabledValidationsConfig

// Wrapper for scheduler steps that validates them before/after execution.
type StepValidator[RequestType PipelineRequest] struct {
	// The wrapped step to validate.
	Step Step[RequestType]
	// By default, we execute all validations. However, through the config,
	// we can also disable some validations if necessary.
	DisabledValidations disabledValidations
}

// Get the name of the wrapped step.
func (s *StepValidator[RequestType]) GetName() string {
	return s.Step.GetName()
}

// Initialize the wrapped step with the database and options.
func (s *StepValidator[RequestType]) Init(db db.DB, opts conf.RawOpts) error {
	slog.Info(
		"scheduler: init validation for step", "name", s.GetName(),
		"disabled", s.DisabledValidations,
	)
	return s.Step.Init(db, opts)
}

// Validate the wrapped step with the database and options.
func ValidateStep[RequestType PipelineRequest](step Step[RequestType], disabledValidations disabledValidations) *StepValidator[RequestType] {
	return &StepValidator[RequestType]{
		Step:                step,
		DisabledValidations: disabledValidations,
	}
}

// Run the step and validate what happens.
func (s *StepValidator[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	result, err := s.Step.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	// If not disabled, validate that the number of subjects stayed the same.
	if !s.DisabledValidations.SameSubjectNumberInOut {
		if len(result.Activations) != len(request.GetSubjects()) {
			return nil, errors.New("number of subjects changed during step execution")
		}
	}
	return result, nil
}

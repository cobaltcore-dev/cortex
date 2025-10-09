// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"log/slog"

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
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

// Get the alias of the wrapped step.
func (s *StepValidator[RequestType]) GetAlias() string {
	return s.Step.GetAlias()
}

// Initialize the wrapped step with the database and options.
func (s *StepValidator[RequestType]) Init(alias string, db db.DB, opts libconf.RawOpts) error {
	slog.Info(
		"scheduler: init validation for step", "name", s.GetName(),
		"disabled", s.DisabledValidations,
	)
	return s.Step.Init(alias, db, opts)
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
	// Note that for some schedulers the same subject (e.g. compute host) may
	// appear multiple times if there is a substruct (e.g. hypervisor hostname).
	// Since cortex will only schedule on the subject level and not below,
	// we need to deduplicate the subjects first before the validation.
	if !s.DisabledValidations.SameSubjectNumberInOut {
		deduplicated := map[string]struct{}{}
		for _, subject := range request.GetSubjects() {
			deduplicated[subject] = struct{}{}
		}
		if len(result.Activations) != len(deduplicated) {
			return nil, errors.New("safety: number of (deduplicated) subjects changed during step execution")
		}
	}
	// If not disabled, validate that some subjects remain.
	if !s.DisabledValidations.SomeSubjectsRemain {
		if len(result.Activations) == 0 {
			return nil, errors.New("safety: no subjects remain after step execution")
		}
	}
	return result, nil
}

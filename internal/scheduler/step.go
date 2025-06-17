// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

var (
	// This error is returned from the step at any time when the step should be skipped.
	ErrStepSkipped = errors.New("step skipped")
)

// Interface to which step options must conform.
type StepOpts interface {
	// Validate the options for this step.
	Validate() error
}

// Interface for a scheduler step.
type Step[RequestType PipelineRequest] interface {
	// Configure the step with a database and options.
	Init(db db.DB, opts conf.RawOpts) error
	// Run this step of the scheduling pipeline.
	// Return a map of keys to activation values. Important: keys that are
	// not in the map are considered as filtered out.
	// Provide a traceLog that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request RequestType) (*StepResult, error)
	// Get the name of this step.
	// The name is used to identify the step in metrics, config, logs, and more.
	// Should be something like: "my_cool_scheduler_step".
	GetName() string
}

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[RequestType PipelineRequest, Opts StepOpts] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The activation function to use.
	ActivationFunction
	// Database connection.
	DB db.DB
}

// Init the step with the database and options.
func (s *BaseStep[RequestType, Opts]) Init(db db.DB, opts conf.RawOpts) error {
	if err := s.Load(opts); err != nil {
		return err
	}
	s.DB = db
	return s.Options.Validate()
}

// Get a default result (no action) for the input weight keys given in the request.
func (s *BaseStep[RequestType, Opts]) PrepareResult(request RequestType) *StepResult {
	activations := make(map[string]float64)
	for _, subject := range request.GetSubjects() {
		activations[subject] = s.NoEffect()
	}
	stats := make(map[string]StepStatistics)
	return &StepResult{Activations: activations, Statistics: stats}
}

// Get default statistics for the input weight keys given in the request.
func (s *BaseStep[RequestType, Opts]) PrepareStats(request PipelineRequest, unit string) StepStatistics {
	return StepStatistics{
		Unit:     unit,
		Subjects: make(map[string]float64, len(request.GetSubjects())),
	}
}

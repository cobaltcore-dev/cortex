// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

// Interface to which step options must conform.
type StepOpts interface {
	// Validate the options for this step.
	Validate() error
}

// Empty options for steps that don't need any.
type EmptyStepOpts struct{}

func (o EmptyStepOpts) Validate() error { return nil }

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[Opts StepOpts] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The activation function to use.
	scheduler.ActivationFunction
	// Database connection.
	DB db.DB
}

// Init the step with the database and options.
func (s *BaseStep[Opts]) Init(db db.DB, opts conf.RawOpts) error {
	if err := s.Load(opts); err != nil {
		return err
	}
	s.DB = db
	return s.Options.Validate()
}

// Get a default result (no action) for the input hosts given in the request.
func (s *BaseStep[Opts]) PrepareResult(request api.Request) *StepResult {
	activations := make(map[string]float64)
	for _, host := range request.GetHosts() {
		activations[host] = s.NoEffect()
	}
	stats := make(map[string]StepStatistics)
	return &StepResult{Activations: activations, Statistics: stats}
}

// Get default statistics for the input hosts given in the request.
func (s *BaseStep[Opts]) PrepareStats(request api.Request, unit string) StepStatistics {
	return StepStatistics{
		Unit:  unit,
		Hosts: make(map[string]float64, len(request.GetHosts())),
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
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
	ActivationFunction
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

// Get zero activations for all hosts.
func (s *BaseStep[Opts]) BaseActivations(request api.Request) Weights {
	weights := make(Weights)
	for _, host := range request.GetHosts() {
		weights[host] = s.NoEffect()
	}
	return weights
}

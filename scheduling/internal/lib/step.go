// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// Empty step opts conforming to the StepOpts interface (validation always succeeds).
type EmptyStepOpts struct{}

func (EmptyStepOpts) Validate() error { return nil }

// Interface for a scheduler step.
type Step[RequestType PipelineRequest] interface {
	// Configure the step and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step v1alpha1.Step) error
	// Deinitialize the step, freeing any held resources.
	Deinit(ctx context.Context) error
	// Run this step of the scheduling pipeline.
	// Return a map of keys to activation values. Important: keys that are
	// not in the map are considered as filtered out.
	// Provide a traceLog that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[RequestType PipelineRequest, Opts StepOpts] struct {
	// Options to pass via yaml to this step.
	libconf.JsonOpts[Opts]
	// The activation function to use.
	ActivationFunction
	// The kubernetes client to use.
	Client client.Client
	// Initialized database connection, if configured through the step spec.
	DB *db.DB
}

// Init the step with the database and options.
func (s *BaseStep[RequestType, Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	opts := libconf.NewRawOptsBytes(step.Spec.Opts.Raw)
	if err := s.Load(opts); err != nil {
		return err
	}
	if err := s.Options.Validate(); err != nil {
		return err
	}

	if step.Spec.DatabaseSecretRef != nil {
		authenticatedDB, err := db.Connector{Client: client}.
			FromSecretRef(ctx, *step.Spec.DatabaseSecretRef)
		if err != nil {
			return err
		}
		s.DB = authenticatedDB
	}

	s.Client = client
	return nil
}

// Deinitialize the step, freeing any held resources.
func (s *BaseStep[RequestType, Opts]) Deinit(ctx context.Context) error {
	if s.DB != nil {
		s.DB.Close()
	}
	return nil
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

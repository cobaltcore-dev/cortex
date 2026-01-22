// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
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
type Step[RequestType PipelineRequest, StepType v1alpha1.Step] interface {
	// Configure the step and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step StepType) error

	// Run this step of the scheduling pipeline.
	//
	// The request is immutable and modifications are stored in the result.
	// This allows steps to be run in parallel (e.g. weighers) without passing
	// mutable state around.
	//
	// All hosts that should not be filtered out must be included in the returned
	// map of activations. I.e., filters implementing this interface should
	// remove activations by omitting them from the returned map.
	//
	// Weighers implementing this interface should adjust activation
	// values in the returned map, including all hosts from the request.
	//
	// A traceLog is provided that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request RequestType) (*StepResult, error)
}

// Step that acts as a weigher in the scheduling pipeline.
type Weigher[RequestType PipelineRequest] = Step[RequestType, v1alpha1.WeigherSpec]

// Step that acts as a filter in the scheduling pipeline.
type Filter[RequestType PipelineRequest] = Step[RequestType, v1alpha1.FilterSpec]

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[RequestType PipelineRequest, Opts StepOpts, StepType v1alpha1.Step] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The activation function to use.
	ActivationFunction
	// The kubernetes client to use.
	Client client.Client
}

// Common base implementation of a weigher step.
// Functionally identical to BaseStep, but used for clarity.
type BaseWeigher[RequestType PipelineRequest, Opts StepOpts] = BaseStep[RequestType, Opts, v1alpha1.WeigherSpec]

// Common base implementation of a filter step.
// Functionally identical to BaseStep, but used for clarity.
type BaseFilter[RequestType PipelineRequest, Opts StepOpts] = BaseStep[RequestType, Opts, v1alpha1.FilterSpec]

// Init the step with the database and options.
func (s *BaseStep[RequestType, Opts, StepType]) Init(ctx context.Context, client client.Client, step StepType) error {
	opts := conf.NewRawOptsBytes(step.GetOpts().Raw)
	if err := s.Load(opts); err != nil {
		return err
	}
	if err := s.Options.Validate(); err != nil {
		return err
	}

	s.Client = client
	return nil
}

// Get a default result (no action) for the input weight keys given in the request.
// Use this to initialize the result before applying filtering/weighing logic.
func (s *BaseStep[RequestType, Opts, StepType]) IncludeAllHostsFromRequest(request RequestType) *StepResult {
	activations := make(map[string]float64)
	for _, subject := range request.GetSubjects() {
		activations[subject] = s.NoEffect()
	}
	stats := make(map[string]StepStatistics)
	return &StepResult{Activations: activations, Statistics: stats}
}

// Get default statistics for the input weight keys given in the request.
func (s *BaseStep[RequestType, Opts, StepType]) PrepareStats(request RequestType, unit string) StepStatistics {
	return StepStatistics{
		Unit:     unit,
		Subjects: make(map[string]float64, len(request.GetSubjects())),
	}
}

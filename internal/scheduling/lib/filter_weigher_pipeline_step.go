// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Steps can be chained together to form a scheduling pipeline.
type FilterWeigherPipelineStep[RequestType FilterWeigherPipelineRequest] interface {
	// Run this step in the scheduling pipeline.
	//
	// The request is immutable and modifications are stored in the result.
	// This allows steps to be run in parallel (e.g. weighers) without passing
	// mutable state around.
	//
	// All hosts that should not be filtered out must be included in the returned
	// map of activations. I.e., filters implementing this interface should
	// remove activations by omitting them from the returned map.
	//
	// Filters implementing this interface should adjust activation
	// values in the returned map, including all hosts from the request.
	//
	// A traceLog is provided that contains the global request id and should
	// be used to log the step's execution.
	Run(traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error)
}

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseFilterWeigherPipelineStep[RequestType FilterWeigherPipelineRequest, Opts FilterWeigherPipelineStepOpts] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The activation function to use.
	ActivationFunction
	// The kubernetes client to use.
	Client client.Client
}

// Init the step with the database and options.
func (s *BaseFilterWeigherPipelineStep[RequestType, Opts]) Init(ctx context.Context, client client.Client, params runtime.RawExtension) error {
	opts := conf.NewRawOptsBytes(params.Raw)
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
func (s *BaseFilterWeigherPipelineStep[RequestType, Opts]) IncludeAllHostsFromRequest(request RequestType) *FilterWeigherPipelineStepResult {
	activations := make(map[string]float64)
	for _, subject := range request.GetSubjects() {
		activations[subject] = s.NoEffect()
	}
	stats := make(map[string]FilterWeigherPipelineStepStatistics)
	return &FilterWeigherPipelineStepResult{Activations: activations, Statistics: stats}
}

// Get default statistics for the input weight keys given in the request.
func (s *BaseFilterWeigherPipelineStep[RequestType, Opts]) PrepareStats(request RequestType, unit string) FilterWeigherPipelineStepStatistics {
	return FilterWeigherPipelineStepStatistics{
		Unit:     unit,
		Subjects: make(map[string]float64, len(request.GetSubjects())),
	}
}

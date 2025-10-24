// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

type PipelineRequest api.ExternalSchedulerRequest

func (r PipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Hosts))
	for i, host := range r.Hosts {
		hosts[i] = host.VolumeHost
	}
	return hosts
}
func (r PipelineRequest) GetWeights() map[string]float64 {
	return r.Weights
}
func (r PipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{
		slog.String("greq", r.Context.GlobalRequestID),
		slog.String("req", r.Context.RequestID),
		slog.String("user", r.Context.UserID),
		slog.String("project", r.Context.ProjectID),
	}
}
func (r PipelineRequest) GetPipeline() string {
	return r.Pipeline
}
func (r PipelineRequest) WithPipeline(pipeline string) scheduling.PipelineRequest {
	r.Pipeline = pipeline
	return r
}

type CinderStep = scheduling.Step[PipelineRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}

// Create a new Cinder scheduler pipeline.
func NewPipeline(
	config conf.CinderSchedulerPipelineConfig,
	db db.DB,
	monitor scheduling.PipelineMonitor,
) lib.Pipeline[PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduling.StepWrapper[PipelineRequest, struct{}]{
		// Validate that no hosts are removed.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return scheduling.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return scheduling.MonitorStep(s, monitor)
		},
	}
	return lib.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

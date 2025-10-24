// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

type CinderStep = scheduling.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}

// Create a new Cinder scheduler pipeline.
func NewPipeline(
	config conf.CinderSchedulerPipelineConfig,
	db db.DB,
	monitor scheduling.PipelineMonitor,
) lib.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduling.StepWrapper[api.ExternalSchedulerRequest, struct{}]{
		// Validate that no hosts are removed.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return lib.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return lib.MonitorStep(s, monitor)
		},
	}
	return lib.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/cinder/api"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

type CinderStep = scheduling.Step[api.PipelineRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}

// Create a new Cinder scheduler pipeline.
func NewPipeline(
	config conf.CinderSchedulerPipelineConfig,
	db db.DB,
	monitor scheduling.PipelineMonitor,
	mqttClient mqtt.Client,
) scheduling.Pipeline[api.PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduling.StepWrapper[api.PipelineRequest, struct{}]{
		// Validate that no hosts are removed.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return scheduling.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s CinderStep, c conf.CinderSchedulerStepConfig) CinderStep {
			return scheduling.MonitorStep(s, monitor)
		},
	}
	return scheduling.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/cinder/api"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
)

type CinderStep = lib.Step[api.PipelineRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}

const (
	TopicFinished = "cortex/scheduler/cinder/pipeline/finished"
)

// Create a new Cinder scheduler pipeline.
func NewPipeline(
	config conf.CinderSchedulerPipelineConfig,
	db db.DB,
	monitor lib.PipelineMonitor,
	mqttClient mqtt.Client,
) lib.Pipeline[api.PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.PipelineRequest]{
		// Validate that no hosts are removed.
		func(s CinderStep, conf conf.SchedulerStepConfig) CinderStep {
			return lib.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s CinderStep, conf conf.SchedulerStepConfig) CinderStep {
			return lib.MonitorStep(s, monitor)
		},
	}
	return lib.NewPipeline(
		supportedSteps, config.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished,
	)
}

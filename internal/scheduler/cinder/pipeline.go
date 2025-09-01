// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/cinder/api"
)

type CinderStep = scheduler.Step[api.ExternalSchedulerRequest]

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
	monitor scheduler.PipelineMonitor,
	mqttClient mqtt.Client,
) scheduler.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduler.StepWrapper[api.ExternalSchedulerRequest]{
		// Validate that no hosts are removed.
		func(s CinderStep, conf conf.SchedulerStepConfig) CinderStep {
			return scheduler.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s CinderStep, conf conf.SchedulerStepConfig) CinderStep {
			return scheduler.MonitorStep(s, monitor)
		},
	}
	return scheduler.NewPipeline(
		supportedSteps, config.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished,
	)
}

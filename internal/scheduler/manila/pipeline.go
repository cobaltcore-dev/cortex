// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/plugins/netapp"
)

type ManilaStep = scheduler.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() ManilaStep{
	(&netapp.CPUUsageBalancingStep{}).GetName(): func() ManilaStep { return &netapp.CPUUsageBalancingStep{} },
}

// Create a new Manila scheduler pipeline.
func NewPipeline(
	config conf.SchedulerConfig,
	db db.DB,
	monitor scheduler.PipelineMonitor,
	mqttClient mqtt.Client,
) scheduler.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduler.StepWrapper[api.ExternalSchedulerRequest]{
		// Validate that no hosts are removed.
		func(s ManilaStep, conf conf.SchedulerStepConfig) ManilaStep {
			return scheduler.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s ManilaStep, conf conf.SchedulerStepConfig) ManilaStep {
			return scheduler.MonitorStep(s, monitor)
		},
	}
	topicFinished := "cortex/scheduler/manila/pipeline/finished"
	return scheduler.NewPipeline(
		supportedSteps, config.Manila.Plugins, wrappers,
		db, monitor, mqttClient, topicFinished,
	)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/manila/api"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/manila/plugins/netapp"
)

type ManilaStep = lib.Step[api.PipelineRequest]

// Configuration of steps supported by the lib.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() ManilaStep{
	(&netapp.CPUUsageBalancingStep{}).GetName(): func() ManilaStep { return &netapp.CPUUsageBalancingStep{} },
}

const (
	TopicFinished = "cortex/scheduler/manila/pipeline/finished"
)

// Create a new Manila scheduler pipeline.
func NewPipeline(
	config conf.ManilaSchedulerPipelineConfig,
	db db.DB,
	monitor lib.PipelineMonitor,
	mqttClient mqtt.Client,
) lib.Pipeline[api.PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.PipelineRequest]{
		// Validate that no hosts are removed.
		func(s ManilaStep, conf conf.SchedulerStepConfig) ManilaStep {
			return lib.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s ManilaStep, conf conf.SchedulerStepConfig) ManilaStep {
			return lib.MonitorStep(s, monitor)
		},
	}
	return lib.NewPipeline(
		supportedSteps, config.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished,
	)
}

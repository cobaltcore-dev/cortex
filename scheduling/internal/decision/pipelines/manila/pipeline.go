// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/manila/plugins/netapp"
)

type ManilaStep = scheduling.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() ManilaStep{
	(&netapp.CPUUsageBalancingStep{}).GetName(): func() ManilaStep { return &netapp.CPUUsageBalancingStep{} },
}

// Create a new Manila scheduler pipeline.
func NewPipeline(
	config conf.ManilaSchedulerPipelineConfig,
	db db.DB,
	monitor scheduling.PipelineMonitor,
) scheduling.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduling.StepWrapper[api.ExternalSchedulerRequest, struct{}]{
		// Validate that no hosts are removed.
		func(s ManilaStep, c conf.ManilaSchedulerStepConfig) ManilaStep {
			return scheduling.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s ManilaStep, c conf.ManilaSchedulerStepConfig) ManilaStep {
			return scheduling.MonitorStep(s, monitor)
		},
	}
	return scheduling.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

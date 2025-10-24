// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

type MachineStep = lib.Step[ironcore.MachinePipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() MachineStep{
	"noop": func() MachineStep { return &NoopFilter{} },
}

// Create a new machine scheduler pipeline.
func NewPipeline(
	config conf.MachineSchedulerPipelineConfig,
	db db.DB,
	monitor lib.PipelineMonitor,
) lib.Pipeline[ironcore.MachinePipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[ironcore.MachinePipelineRequest, struct{}]{
		// Monitor the step execution.
		func(s MachineStep, c conf.MachineSchedulerStepConfig) MachineStep {
			// This monitor calculates detailed impact metrics for each step.
			return lib.MonitorStep(s, monitor)
		},
	}
	return lib.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

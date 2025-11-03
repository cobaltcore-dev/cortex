// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

type MachineStep = lib.Step[ironcore.MachinePipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() MachineStep{
	"noop": func() MachineStep { return &NoopFilter{} },
}

// Create a new machine scheduler pipeline.
func NewPipeline(
	steps []v1alpha1.Step,
	monitor lib.PipelineMonitor,
) (lib.Pipeline[ironcore.MachinePipelineRequest], error) {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[ironcore.MachinePipelineRequest]{
		// Monitor the step execution.
		func(s MachineStep, config v1alpha1.Step) (MachineStep, error) {
			return lib.MonitorStep(s, monitor), nil
		},
	}
	return lib.NewPipeline(supportedSteps, steps, wrappers, monitor)
}

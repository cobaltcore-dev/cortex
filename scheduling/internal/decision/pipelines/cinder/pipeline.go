// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

type CinderStep = scheduling.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() CinderStep{}

// Create a new Cinder scheduler pipeline.
func NewPipeline(
	steps []v1alpha1.Step,
	db db.DB,
	monitor scheduling.PipelineMonitor,
) (lib.Pipeline[api.ExternalSchedulerRequest], error) {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.ExternalSchedulerRequest]{
		// Validate that no hosts are removed.
		func(s CinderStep, config v1alpha1.Step) (CinderStep, error) {
			if config.Spec.Type != v1alpha1.StepTypeWeigher {
				return s, nil
			}
			if config.Spec.Weigher == nil {
				return s, nil
			}
			return lib.ValidateStep(s, config.Spec.Weigher.DisabledValidations), nil
		},
		// Monitor the step execution.
		func(s CinderStep, config v1alpha1.Step) (CinderStep, error) {
			return lib.MonitorStep(s, monitor), nil
		},
	}
	return lib.NewPipeline(supportedSteps, steps, wrappers, db, monitor)
}

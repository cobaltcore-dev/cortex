// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"encoding/json"
	"errors"

	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/nova/plugins/vmware"
)

type NovaStep = lib.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() NovaStep{
	// VMware-specific steps
	(&vmware.AntiAffinityNoisyProjectsStep{}).GetName():    func() NovaStep { return &vmware.AntiAffinityNoisyProjectsStep{} },
	(&vmware.AvoidLongTermContendedHostsStep{}).GetName():  func() NovaStep { return &vmware.AvoidLongTermContendedHostsStep{} },
	(&vmware.AvoidShortTermContendedHostsStep{}).GetName(): func() NovaStep { return &vmware.AvoidShortTermContendedHostsStep{} },
	// KVM-specific steps
	(&kvm.AvoidOverloadedHostsCPUStep{}).GetName():    func() NovaStep { return &kvm.AvoidOverloadedHostsCPUStep{} },
	(&kvm.AvoidOverloadedHostsMemoryStep{}).GetName(): func() NovaStep { return &kvm.AvoidOverloadedHostsMemoryStep{} },
	// Shared steps
	(&shared.ResourceBalancingStep{}).GetName():         func() NovaStep { return &shared.ResourceBalancingStep{} },
	(&shared.FilterHasAcceleratorsStep{}).GetName():     func() NovaStep { return &shared.FilterHasAcceleratorsStep{} },
	(&shared.FilterCorrectAZStep{}).GetName():           func() NovaStep { return &shared.FilterCorrectAZStep{} },
	(&shared.FilterDisabledStep{}).GetName():            func() NovaStep { return &shared.FilterDisabledStep{} },
	(&shared.FilterPackedVirtqueueStep{}).GetName():     func() NovaStep { return &shared.FilterPackedVirtqueueStep{} },
	(&shared.FilterExternalCustomerStep{}).GetName():    func() NovaStep { return &shared.FilterExternalCustomerStep{} },
	(&shared.FilterProjectAggregatesStep{}).GetName():   func() NovaStep { return &shared.FilterProjectAggregatesStep{} },
	(&shared.FilterComputeCapabilitiesStep{}).GetName(): func() NovaStep { return &shared.FilterComputeCapabilitiesStep{} },
	(&shared.FilterHasRequestedTraits{}).GetName():      func() NovaStep { return &shared.FilterHasRequestedTraits{} },
	(&shared.FilterHasEnoughCapacity{}).GetName():       func() NovaStep { return &shared.FilterHasEnoughCapacity{} },
	(&shared.FilterHostInstructionsStep{}).GetName():    func() NovaStep { return &shared.FilterHostInstructionsStep{} },
}

// Specific pipeline for nova.
type novaPipeline struct {
	// The underlying shared pipeline logic.
	lib.Pipeline[api.ExternalSchedulerRequest]
	// Database to use for the nova pipeline.
	database db.DB
}

// Create a new Nova scheduler pipeline.
func NewPipeline(
	steps []v1alpha1.Step,
	db db.DB,
	monitor lib.PipelineMonitor,
) (lib.Pipeline[api.ExternalSchedulerRequest], error) {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.ExternalSchedulerRequest]{
		// Scope the step to Nova hosts/specs that match the step's scope.
		func(s NovaStep, config v1alpha1.Step) (NovaStep, error) {
			if len(config.Spec.Opts.Raw) == 0 {
				return s, nil
			}
			var c NovaSchedulerOpts
			if err := json.Unmarshal(config.Spec.Opts.Raw, &c); err != nil {
				return nil, errors.New("failed to unmarshal nova scheduler step opts: " + err.Error())
			}
			return &StepScoper{Step: s, Scope: c.Scope}, nil
		},
		func(s NovaStep, config v1alpha1.Step) (NovaStep, error) {
			if config.Spec.Type != v1alpha1.StepTypeWeigher {
				return s, nil
			}
			if config.Spec.Weigher == nil {
				return s, nil
			}
			return lib.ValidateStep(s, config.Spec.Weigher.DisabledValidations), nil
		},
		// Monitor the step execution.
		func(s NovaStep, config v1alpha1.Step) (NovaStep, error) {
			return lib.MonitorStep(s, monitor), nil
		},
	}
	pipeline, err := lib.NewPipeline(supportedSteps, steps, wrappers, db, monitor)
	if err != nil {
		return nil, err
	}
	return &novaPipeline{pipeline, db}, nil
}

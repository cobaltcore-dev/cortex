// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
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
	config conf.NovaSchedulerPipelineConfig,
	db db.DB,
	monitor lib.PipelineMonitor,
) lib.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.ExternalSchedulerRequest, conf.NovaSchedulerStepExtraConfig]{
		// Scope the step to Nova hosts/specs that match the step's scope.
		func(s NovaStep, c conf.NovaSchedulerStepConfig) NovaStep {
			if c.Extra == nil {
				return s // No Nova configuration, run the step as is.
			}
			return &StepScoper{Step: s, Scope: (*c.Extra).Scope}
		},
		// Validate that no hosts are removed.
		func(s NovaStep, c conf.NovaSchedulerStepConfig) NovaStep {
			return lib.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s NovaStep, c conf.NovaSchedulerStepConfig) NovaStep {
			return lib.MonitorStep(s, monitor)
		},
	}
	pipeline := lib.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
	return &novaPipeline{pipeline, db}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/vmware"
)

type NovaStep = scheduler.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var steps = []NovaStep{
	// VMware-specific steps
	&vmware.AntiAffinityNoisyProjectsStep{},
	&vmware.AvoidLongTermContendedHostsStep{},
	&vmware.AvoidShortTermContendedHostsStep{},
	// KVM-specific steps
	&kvm.AvoidOverloadedHostsCPUStep{},
	&kvm.AvoidOverloadedHostsMemoryStep{},
	// Shared steps
	&shared.ResourceBalancingStep{},
}

// Create a new Nova scheduler pipeline.
func NewPipeline(
	config conf.SchedulerConfig,
	db db.DB,
	monitor scheduler.PipelineMonitor,
	mqttClient mqtt.Client,
) scheduler.Pipeline[api.ExternalSchedulerRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduler.StepWrapper[api.ExternalSchedulerRequest]{
		// Scope the step to Nova hosts/specs that match the step's scope.
		func(s NovaStep, c conf.SchedulerStepConfig) NovaStep {
			if c.Scope == nil {
				return s // No Nova configuration, run the step as is.
			}
			return &StepScoper{Step: s, Scope: *c.Scope}
		},
		// Validate that no hosts are removed.
		func(s NovaStep, conf conf.SchedulerStepConfig) NovaStep {
			return scheduler.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s NovaStep, conf conf.SchedulerStepConfig) NovaStep {
			return scheduler.MonitorStep(s, monitor)
		},
	}
	topicFinished := "cortex/scheduler/nova/pipeline/finished"
	return scheduler.NewPipeline(
		steps, config.Nova.Plugins, wrappers, config,
		db, monitor, mqttClient, topicFinished,
	)
}

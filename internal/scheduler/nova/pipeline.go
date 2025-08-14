// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

type NovaStep = scheduler.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduler.
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

const (
	TopicFinished = "cortex/scheduler/nova/pipeline/finished"
)

// Modifier for the pipeline request that is executed before the pipeline itself.
type premodifier struct {
	database db.DB
	// Config that applies to the nova pipeline premodifier.
	config conf.NovaSchedulerConfig
}

// If configured, modify the request before it is sent to the pipeline.
func (p *premodifier) ModifyRequest(request *api.ExternalSchedulerRequest) error {
	if p.config.PreselectAllHosts {
		// Get all available hypervisors from the database.
		var hypervisors []nova.Hypervisor
		if _, err := p.database.Select(
			&hypervisors, "SELECT * FROM "+nova.Hypervisor{}.TableName(),
		); err != nil {
			return err
		}
		if len(hypervisors) == 0 {
			return errors.New("no hypervisors found")
		}
		request.Hosts = make([]api.ExternalSchedulerHost, 0, len(hypervisors))
		request.Weights = make(map[string]float64, len(hypervisors))
		for _, hypervisor := range hypervisors {
			request.Hosts = append(request.Hosts, api.ExternalSchedulerHost{
				ComputeHost:        hypervisor.ServiceHost,
				HypervisorHostname: hypervisor.Hostname,
			})
			request.Weights[hypervisor.ServiceHost] = 0.0
		}
		slog.Info("preselecting all hosts for Nova pipeline", "hosts", len(request.Hosts))
	}
	return nil
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
	premodifier := &premodifier{
		database: db,
		config:   config.Nova,
	}
	return scheduler.NewPipeline(
		supportedSteps, config.Nova.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished, premodifier,
	)
}

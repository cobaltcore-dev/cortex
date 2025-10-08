// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/plugins/vmware"
)

type NovaStep = lib.Step[api.PipelineRequest]

// Configuration of steps supported by the lib.
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

// Specific pipeline for nova.
type novaPipeline struct {
	// The underlying shared pipeline logic.
	lib.Pipeline[api.PipelineRequest]
	// Database to use for the nova pipeline.
	database db.DB
	// Whether the pipeline should preselect all hosts.
	// This will override hosts provided by the user.
	preselectAllHosts bool
}

// Create a new Nova scheduler pipeline.
func NewPipeline(
	config conf.NovaSchedulerPipelineConfig,
	db db.DB,
	monitor lib.PipelineMonitor,
	mqttClient mqtt.Client,
) lib.Pipeline[api.PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.PipelineRequest]{
		// Scope the step to Nova hosts/specs that match the step's scope.
		func(s NovaStep, c conf.SchedulerStepConfig) NovaStep {
			if c.Scope == nil {
				return s // No Nova configuration, run the step as is.
			}
			return &StepScoper{Step: s, Scope: *c.Scope}
		},
		// Validate that no hosts are removed.
		func(s NovaStep, conf conf.SchedulerStepConfig) NovaStep {
			return lib.ValidateStep(s, conf.DisabledValidations)
		},
		// Monitor the step execution.
		func(s NovaStep, conf conf.SchedulerStepConfig) NovaStep {
			return lib.MonitorStep(s, monitor)
		},
	}
	pipeline := lib.NewPipeline(
		supportedSteps, config.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished,
	)
	return &novaPipeline{pipeline, db, config.PreselectAllHosts}
}

// If needed, modify the request before sending it off to the pipeline.
func (p *novaPipeline) modify(request *api.PipelineRequest) error {
	if p.preselectAllHosts {
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
		request.Hosts = make([]delegationAPI.ExternalSchedulerHost, 0, len(hypervisors))
		request.Weights = make(map[string]float64, len(hypervisors))
		for _, hypervisor := range hypervisors {
			request.Hosts = append(request.Hosts, delegationAPI.ExternalSchedulerHost{
				ComputeHost:        hypervisor.ServiceHost,
				HypervisorHostname: hypervisor.Hostname,
			})
			request.Weights[hypervisor.ServiceHost] = 0.0
		}
		slog.Info("preselecting all hosts for Nova pipeline", "hosts", len(request.Hosts))
	}
	return nil
}

// Run the pipeline logic with additional actions for nova.
func (p *novaPipeline) Run(request api.PipelineRequest) ([]string, error) {
	// Modify the request to use the nova client.
	if err := p.modify(&request); err != nil {
		return nil, err
	}
	return p.Pipeline.Run(request)
}

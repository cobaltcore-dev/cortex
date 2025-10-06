// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// Specific pipeline for nova.
type novaPipeline struct {
	// The underlying shared pipeline logic.
	scheduler.Pipeline[api.ExternalSchedulerRequest]
	// Database to use for the nova pipeline.
	database db.DB
	// Whether the pipeline should preselect all hosts.
	// This will override hosts provided by the user.
	preselectAllHosts bool
}

type novaPipelineConsumer struct {
	// Kubernetes client to create decision resources.
	Client client.Client
}

func NewNovaPipelineConsumer() *novaPipelineConsumer {
	var kubernetesClient client.Client
	if scheme, err := v1alpha1.SchemeBuilder.Build(); err == nil {
		if clientConfig, err := ctrl.GetConfig(); err == nil {
			if cl, err := client.New(clientConfig, client.Options{Scheme: scheme}); err == nil {
				// Successfully created a client, use it.
				kubernetesClient = cl
			}
		}
	}
	return &novaPipelineConsumer{
		Client: kubernetesClient,
	}
}

func (c *novaPipelineConsumer) Consume(
	request api.ExternalSchedulerRequest,
	applicationOrder []string,
	inWeights map[string]float64,
	stepWeights map[string]map[string]float64,
) {

	if c.Client == nil {
		return
	}

	// Determine the event type based on request flags
	var eventType v1alpha1.SchedulingEventType
	switch {
	case request.Live:
		eventType = v1alpha1.SchedulingEventTypeLiveMigration
	case request.Resize:
		eventType = v1alpha1.SchedulingEventTypeResize
	default:
		eventType = v1alpha1.SchedulingEventTypeInitialPlacement
	}

	outputs := []v1alpha1.SchedulingDecisionPipelineOutputSpec{}
	for _, stepKey := range applicationOrder {
		weights, ok := stepWeights[stepKey]
		if !ok {
			// This is ok, since steps can be skipped.
			continue
		}
		activations := make(map[string]float64, len(weights))
		for k, v := range weights {
			activations[k] = math.Tanh(v)
		}
		outputs = append(outputs, v1alpha1.SchedulingDecisionPipelineOutputSpec{
			Step:        stepKey,
			Activations: activations,
		})
	}

	flavor := request.Spec.Data.Flavor

	// Safe conversion with bounds checking to prevent integer overflow
	vcpus := int(math.Min(float64(flavor.Data.VCPUs), math.MaxInt))
	ram := int(math.Min(float64(flavor.Data.MemoryMB), math.MaxInt))
	disk := int(math.Min(float64(flavor.Data.RootGB), math.MaxInt))

	decisionRequest := v1alpha1.SchedulingDecisionRequest{
		ID:          request.Spec.Data.InstanceUUID,
		RequestedAt: metav1.Now(),
		EventType:   eventType,
		Input:       inWeights,
		Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
			Name:    request.GetPipeline(),
			Outputs: outputs,
		},
		AvailabilityZone: request.Spec.Data.AvailabilityZone,
		VMware:           request.VMware,
		Flavor: v1alpha1.Flavor{
			Name:  flavor.Data.Name,
			VCPUs: vcpus,
			RAM:   ram,
			Disk:  disk,
		},
	}

	objectKey := client.ObjectKey{Name: request.Spec.Data.InstanceUUID}

	// Try to update existing decision first
	var existing v1alpha1.SchedulingDecision
	if err := c.Client.Get(context.Background(), objectKey, &existing); err == nil {
		// Decision already exists, append the new decision to the existing ones
		existing.Spec.Decisions = append(existing.Spec.Decisions, decisionRequest)

		if err := c.Client.Update(context.Background(), &existing); err != nil {
			slog.Error("scheduler: failed to update existing decision", "error", err, "resourceID", request.Spec.Data.InstanceUUID)
			return
		}
		slog.Info("scheduler: appended decision to existing resource", "resourceID", request.Spec.Data.InstanceUUID, "eventType", eventType)
		return
	}

	// Decision doesn't exist, create a new one
	decision := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{Name: request.Spec.Data.InstanceUUID},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Decisions: []v1alpha1.SchedulingDecisionRequest{decisionRequest},
		},
		// Status will be filled in by the controller.
	}
	if err := c.Client.Create(context.Background(), decision); err != nil {
		slog.Error("scheduler: failed to create decision", "error", err, "resourceID", request.Spec.Data.InstanceUUID)
		return
	}
	slog.Info("scheduler: created new decision", "resourceID", request.Spec.Data.InstanceUUID, "eventType", eventType)
}

// Create a new Nova scheduler pipeline.
func NewPipeline(
	config conf.NovaSchedulerPipelineConfig,
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
	pipeline := scheduler.NewPipeline(
		supportedSteps, config.Plugins, wrappers,
		db, monitor, mqttClient, TopicFinished,
	)
	wrapped := &novaPipeline{pipeline, db, config.PreselectAllHosts}
	wrapped.SetConsumer(NewNovaPipelineConsumer())
	return wrapped
}

// If needed, modify the request before sending it off to the pipeline.
func (p *novaPipeline) modify(request *api.ExternalSchedulerRequest) error {
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

// Run the pipeline logic with additional actions for nova.
func (p *novaPipeline) Run(request api.ExternalSchedulerRequest) ([]string, error) {
	// Modify the request to use the nova client.
	if err := p.modify(&request); err != nil {
		return nil, err
	}
	return p.Pipeline.Run(request)
}

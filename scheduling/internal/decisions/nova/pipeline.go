// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decisions/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decisions/nova/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NovaStep = lib.Step[api.ExternalSchedulerRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() NovaStep{
	// VMware-specific steps
	"vmware_anti_affinity_noisy_projects":     func() NovaStep { return &vmware.AntiAffinityNoisyProjectsStep{} },
	"vmware_avoid_long_term_contended_hosts":  func() NovaStep { return &vmware.AvoidLongTermContendedHostsStep{} },
	"vmware_avoid_short_term_contended_hosts": func() NovaStep { return &vmware.AvoidShortTermContendedHostsStep{} },
	// Shared steps
	"shared_resource_balancing":   func() NovaStep { return &shared.ResourceBalancingStep{} },
	"filter_has_accelerators":     func() NovaStep { return &shared.FilterHasAcceleratorsStep{} },
	"filter_correct_az":           func() NovaStep { return &shared.FilterCorrectAZStep{} },
	"filter_disabled":             func() NovaStep { return &shared.FilterDisabledStep{} },
	"filter_packed_virtqueue":     func() NovaStep { return &shared.FilterPackedVirtqueueStep{} },
	"filter_external_customer":    func() NovaStep { return &shared.FilterExternalCustomerStep{} },
	"filter_project_aggregates":   func() NovaStep { return &shared.FilterProjectAggregatesStep{} },
	"filter_compute_capabilities": func() NovaStep { return &shared.FilterComputeCapabilitiesStep{} },
	"filter_has_requested_traits": func() NovaStep { return &shared.FilterHasRequestedTraits{} },
	"filter_has_enough_capacity":  func() NovaStep { return &shared.FilterHasEnoughCapacity{} },
	"filter_host_instructions":    func() NovaStep { return &shared.FilterHostInstructionsStep{} },
}

// Specific pipeline for nova.
type novaPipeline struct {
	// The underlying shared pipeline logic.
	lib.Pipeline[api.ExternalSchedulerRequest]
}

// Create a new Nova scheduler pipeline.
func NewPipeline(
	ctx context.Context,
	cl client.Client,
	steps []v1alpha1.Step,
	monitor lib.PipelineMonitor,
) (lib.Pipeline[api.ExternalSchedulerRequest], error) {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []lib.StepWrapper[api.ExternalSchedulerRequest]{
		func(
			ctx context.Context,
			client client.Client,
			step v1alpha1.Step,
			impl lib.Step[api.ExternalSchedulerRequest],
		) (NovaStep, error) {
			if step.Spec.Type != v1alpha1.StepTypeWeigher {
				return impl, nil
			}
			if step.Spec.Weigher == nil {
				return impl, nil
			}
			return lib.ValidateStep(impl, step.Spec.Weigher.DisabledValidations), nil
		},
		func(
			ctx context.Context,
			client client.Client,
			step v1alpha1.Step,
			impl lib.Step[api.ExternalSchedulerRequest],
		) (NovaStep, error) {
			return lib.MonitorStep(ctx, client, step, impl, monitor), nil
		},
	}
	pipeline, err := lib.NewPipeline(ctx, cl, supportedSteps, steps, wrappers, monitor)
	if err != nil {
		return nil, err
	}
	return &novaPipeline{pipeline}, nil
}

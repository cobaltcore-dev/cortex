// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type VMwareAvoidShortTermContendedHostsStepOpts struct {
	AvgCPUContentionLowerBound float64 `json:"avgCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUContentionUpperBound float64 `json:"avgCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUContentionActivationLowerBound float64 `json:"avgCPUContentionActivationLowerBound"`
	AvgCPUContentionActivationUpperBound float64 `json:"avgCPUContentionActivationUpperBound"`

	MaxCPUContentionLowerBound float64 `json:"maxCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUContentionUpperBound float64 `json:"maxCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUContentionActivationLowerBound float64 `json:"maxCPUContentionActivationLowerBound"`
	MaxCPUContentionActivationUpperBound float64 `json:"maxCPUContentionActivationUpperBound"`
}

func (o VMwareAvoidShortTermContendedHostsStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUContentionLowerBound == o.AvgCPUContentionUpperBound {
		return errors.New("avgCPUContentionLowerBound and avgCPUContentionUpperBound must not be equal")
	}
	if o.MaxCPUContentionLowerBound == o.MaxCPUContentionUpperBound {
		return errors.New("maxCPUContentionLowerBound and maxCPUContentionUpperBound must not be equal")
	}
	return nil
}

// Step to avoid recently contended hosts by downvoting them.
type VMwareAvoidShortTermContendedHostsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseWeigher[api.ExternalSchedulerRequest, VMwareAvoidShortTermContendedHostsStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *VMwareAvoidShortTermContendedHostsStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx, corev1.ObjectReference{Name: "vmware-short-term-contended-hosts"}); err != nil {
		return err
	}
	return nil
}

// Downvote hosts that are highly contended.
func (s *VMwareAvoidShortTermContendedHostsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	if !request.VMware {
		slog.Debug("Skipping general purpose balancing step for non-VMware VM")
		return result, nil
	}

	result.Statistics["avg cpu contention"] = s.PrepareStats(request, "%")
	result.Statistics["max cpu contention"] = s.PrepareStats(request, "%")

	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vmware-short-term-contended-hosts"},
		knowledge,
	); err != nil {
		return nil, err
	}
	highlyContendedHosts, err := v1alpha1.
		UnboxFeatureList[compute.VROpsHostsystemContentionShortTerm](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}

	// Push the VM away from highly contended hosts.
	for _, host := range highlyContendedHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[host.ComputeHost]; !ok {
			continue
		}
		activationAvg := lib.MinMaxScale(
			host.AvgCPUContention,
			s.Options.AvgCPUContentionLowerBound,
			s.Options.AvgCPUContentionUpperBound,
			s.Options.AvgCPUContentionActivationLowerBound,
			s.Options.AvgCPUContentionActivationUpperBound,
		)
		activationMax := lib.MinMaxScale(
			host.MaxCPUContention,
			s.Options.MaxCPUContentionLowerBound,
			s.Options.MaxCPUContentionUpperBound,
			s.Options.MaxCPUContentionActivationLowerBound,
			s.Options.MaxCPUContentionActivationUpperBound,
		)
		result.Activations[host.ComputeHost] = activationAvg + activationMax
		result.Statistics["avg cpu contention"].Hosts[host.ComputeHost] = host.AvgCPUContention
		result.Statistics["max cpu contention"].Hosts[host.ComputeHost] = host.MaxCPUContention
	}
	return result, nil
}

func init() {
	Index["vmware_avoid_short_term_contended_hosts"] = func() lib.Weigher[api.ExternalSchedulerRequest] {
		return &VMwareAvoidShortTermContendedHostsStep{}
	}
}

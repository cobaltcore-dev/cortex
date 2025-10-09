// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type AvoidShortTermContendedHostsStepOpts struct {
	AvgCPUContentionLowerBound float64 `json:"avgCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUContentionUpperBound float64 `json:"avgCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUContentionActivationLowerBound float64 `json:"avgCPUContentionActivationLowerBound"`
	AvgCPUContentionActivationUpperBound float64 `json:"avgCPUContentionActivationUpperBound"`

	MaxCPUContentionLowerBound float64 `json:"maxCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUContentionUpperBound float64 `json:"maxCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUContentionActivationLowerBound float64 `json:"maxCPUContentionActivationLowerBound"`
	MaxCPUContentionActivationUpperBound float64 `json:"maxCPUContentionActivationUpperBound"`
}

func (o AvoidShortTermContendedHostsStepOpts) Validate() error {
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
type AvoidShortTermContendedHostsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseStep[api.PipelineRequest, AvoidShortTermContendedHostsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidShortTermContendedHostsStep) GetName() string {
	return "vmware_avoid_short_term_contended_hosts"
}

// Downvote hosts that are highly contended.
func (s *AvoidShortTermContendedHostsStep) Run(traceLog *slog.Logger, request api.PipelineRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["avg cpu contention"] = s.PrepareStats(request, "%")
	result.Statistics["max cpu contention"] = s.PrepareStats(request, "%")

	var highlyContendedHosts []vmware.VROpsHostsystemContentionShortTerm
	group := "scheduler-nova"
	table := vmware.VROpsHostsystemContentionShortTerm{}.TableName()
	if _, err := s.DB.SelectTimed(group, &highlyContendedHosts,
		"SELECT * FROM "+table,
	); err != nil {
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
		result.Statistics["avg cpu contention"].Subjects[host.ComputeHost] = host.AvgCPUContention
		result.Statistics["max cpu contention"].Subjects[host.ComputeHost] = host.MaxCPUContention
	}
	return result, nil
}

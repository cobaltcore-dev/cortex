// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type AvoidLongTermContendedHostsStepOpts struct {
	AvgCPUContentionLowerBound float64 `json:"avgCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUContentionUpperBound float64 `json:"avgCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUContentionActivationLowerBound float64 `json:"avgCPUContentionActivationLowerBound"`
	AvgCPUContentionActivationUpperBound float64 `json:"avgCPUContentionActivationUpperBound"`

	MaxCPUContentionLowerBound float64 `json:"maxCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUContentionUpperBound float64 `json:"maxCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUContentionActivationLowerBound float64 `json:"maxCPUContentionActivationLowerBound"`
	MaxCPUContentionActivationUpperBound float64 `json:"maxCPUContentionActivationUpperBound"`
}

func (o AvoidLongTermContendedHostsStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUContentionLowerBound == o.AvgCPUContentionUpperBound {
		return errors.New("avgCPUContentionLowerBound and avgCPUContentionUpperBound must not be equal")
	}
	if o.MaxCPUContentionLowerBound == o.MaxCPUContentionUpperBound {
		return errors.New("maxCPUContentionLowerBound and maxCPUContentionUpperBound must not be equal")
	}
	return nil
}

// Step to avoid long term contended hosts by downvoting them.
type AvoidLongTermContendedHostsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidLongTermContendedHostsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidLongTermContendedHostsStep) GetName() string {
	return "vmware_avoid_long_term_contended_hosts"
}

// Downvote hosts that are highly contended.
func (s *AvoidLongTermContendedHostsStep) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["avg cpu contention"] = s.PrepareStats(request, "%")
	result.Statistics["max cpu contention"] = s.PrepareStats(request, "%")

	if !request.GetVMware() {
		// Only run this step for VMware VMs.
		return result, nil
	}

	var highlyContendedHosts []vmware.VROpsHostsystemContentionLongTerm
	if _, err := s.DB.Select(&highlyContendedHosts, `
		SELECT * FROM feature_vrops_hostsystem_contention_long_term
	`); err != nil {
		return nil, err
	}

	// Push the VM away from highly contended hosts.
	for _, host := range highlyContendedHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[host.ComputeHost]; !ok {
			continue
		}
		activationAvg := scheduler.MinMaxScale(
			host.AvgCPUContention,
			s.Options.AvgCPUContentionLowerBound,
			s.Options.AvgCPUContentionUpperBound,
			s.Options.AvgCPUContentionActivationLowerBound,
			s.Options.AvgCPUContentionActivationUpperBound,
		)
		activationMax := scheduler.MinMaxScale(
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

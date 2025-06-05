// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

type StepScoper struct {
	// The wrapped step to scope.
	Step plugins.Step
	// The scope for this step.
	Scope conf.NovaSchedulerStepScope
	// The database to use for querying host capabilities.
	DB db.DB
}

func scopeStep[S plugins.Step](step S, scope conf.NovaSchedulerStepScope) *StepScoper {
	return &StepScoper{Step: step, Scope: scope}
}

// Get the name of the wrapped step.
func (s *StepScoper) GetName() string {
	return s.Step.GetName()
}

// Initialize the wrapped step with the database and options.
func (s *StepScoper) Init(db db.DB, opts conf.RawOpts) error {
	slog.Info("scheduler: init scope for step", "name", s.GetName())
	s.DB = db
	return s.Step.Init(db, opts)
}

// Run the step and scope it.
func (s *StepScoper) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result, err := s.Step.Run(traceLog, request)
	if err != nil {
		return nil, err
	}

	// Query hosts in scope.
	hostsInScope, hostsNotInScope, err := s.queryHostsInScope(request)
	if err != nil {
		return nil, err
	}
	// For all hosts not in scope, reset their activations to the no-effect value.
	activationFunction := scheduler.ActivationFunction{}
	for host := range result.Activations {
		// We can use the in-scope or the not-in-scope map here.
		// Its more likely that the hosts out of scope are fewer than
		// the hosts in scope, thus we should use the smaller map.
		if _, ok := hostsNotInScope[host]; ok {
			result.Activations[host] = activationFunction.NoEffect()
		}
	}
	slog.Info(
		"scheduler: scoped step activations",
		"step", s.GetName(),
		"hosts not in scope", hostsNotInScope,
		"hosts in scope", hostsInScope,
	)

	// If the spec is not in scope, reset all activations to the no-effect value.
	if !s.isSpecInScope(traceLog, request) {
		for host := range result.Activations {
			result.Activations[host] = activationFunction.NoEffect()
		}
		slog.Info(
			"scheduler: spec not in scope, resetting activations",
			"step", s.GetName(),
		)
	}

	return result, nil
}

func (s *StepScoper) queryHostsInScope(request api.Request) (
	hostsInScope map[string]struct{},
	hostsNotInScope map[string]struct{},
	err error,
) {
	// If there is no scope, all hosts are in scope.
	if s.Scope.HostCapabilities.IsUndefined() {
		hosts := make(map[string]struct{}, len(request.GetHosts()))
		for _, host := range request.GetHosts() {
			hosts[host] = struct{}{}
		}
		return hosts, nil, nil
	}
	var hostCapabilities []shared.HostCapabilities
	if _, err := s.DB.Select(
		&hostCapabilities, "SELECT * FROM "+shared.HostCapabilities{}.TableName(),
	); err != nil {
		return nil, nil, err
	}
	// Filter hosts based on the scope.
	hostsInScope = make(map[string]struct{})
	for _, host := range hostCapabilities {
		// Check if the host matches ALL the traits if given.
		if len(s.Scope.HostCapabilities.AllOfTraitInfixes) > 0 {
			for _, trait := range s.Scope.HostCapabilities.AllOfTraitInfixes {
				// host.Traits is a comma-separated string of traits.
				if !strings.Contains(host.Traits, trait) {
					continue
				}
			}
		}
		// Check if the host matches ANY of the traits if given.
		if len(s.Scope.HostCapabilities.AnyOfTraitInfixes) > 0 {
			match := false
			for _, trait := range s.Scope.HostCapabilities.AnyOfTraitInfixes {
				// host.Traits is a comma-separated string of traits.
				if strings.Contains(host.Traits, trait) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		// Check if the host matches ANY of the hypervisor types if given.
		if len(s.Scope.HostCapabilities.AnyOfHypervisorTypeInfixes) > 0 {
			match := false
			for _, hypervisorType := range s.Scope.HostCapabilities.AnyOfHypervisorTypeInfixes {
				// host.HypervisorType is a string representing the hypervisor type.
				if strings.Contains(host.HypervisorType, hypervisorType) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		// If the host matches all criteria, add it to the scope.
		hostsInScope[host.ComputeHost] = struct{}{}
	}

	// Check if the selection should be inverted.
	hostsNotInScope = make(map[string]struct{})
	for _, host := range request.GetHosts() {
		if _, ok := hostsInScope[host]; !ok {
			// If the host is not in scope, add it to the not in scope map.
			hostsNotInScope[host] = struct{}{}
		}
	}
	if s.Scope.HostCapabilities.InvertSelection {
		// If the selection is inverted, swap the maps.
		hostsInScope, hostsNotInScope = hostsNotInScope, hostsInScope
	}

	return hostsInScope, hostsNotInScope, nil
}

func (s *StepScoper) isSpecInScope(traceLog *slog.Logger, request api.Request) bool {
	// If there is no scope, the spec is in scope.
	if s.Scope.Spec.IsUndefined() {
		return true
	}
	// Check if the flavor is in scope.
	flavorName := request.GetSpec().Data.Flavor.Data.Name
	if len(s.Scope.Spec.AllOfFlavorNameInfixes) > 0 {
		for _, flavorInfix := range s.Scope.Spec.AllOfFlavorNameInfixes {
			// Check if the flavor name contains the infix.
			if !strings.Contains(flavorName, flavorInfix) {
				// Skip this step if the flavor does not contain the infix.
				traceLog.Info("flavor not in scope", "flavor", flavorName, "infix", flavorInfix)
				return false
			}
		}
	}
	// Additional checks can be added here based on the scope.
	return true
}

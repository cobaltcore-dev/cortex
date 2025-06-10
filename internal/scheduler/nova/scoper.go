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
	hostsInScope, hostsNotInScope, err := s.queryHostsInScope(traceLog, request)
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
	traceLog.Info(
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
		traceLog.Info(
			"scheduler: spec not in scope, resetting activations",
			"step", s.GetName(),
		)
	}

	return result, nil
}

// Based on the provided host selectors, determine which hosts are in scope
// and which are not. The hosts in scope are returned in the first map,
// while the hosts not in scope are returned in the second map.
func (s *StepScoper) queryHostsInScope(traceLog *slog.Logger, request api.Request) (
	hostsInScope map[string]struct{},
	hostsNotInScope map[string]struct{},
	err error,
) {

	hostsInRequest := request.GetHosts()

	// Initially, all hosts in the request are considered in scope.
	hostsInScope = make(map[string]struct{})
	for _, host := range hostsInRequest {
		hostsInScope[host] = struct{}{}
	}

	// If there are no host selectors, return all hosts in the request.
	if len(s.Scope.HostSelectors) == 0 {
		return hostsInScope, hostsNotInScope, nil
	}

	// Fetch the host capabilities.
	var hostCapabilities []shared.HostCapabilities
	if _, err := s.DB.Select(
		&hostCapabilities, "SELECT * FROM "+shared.HostCapabilities{}.TableName(),
	); err != nil {
		return nil, nil, err
	}
	capabilityByHost := make(map[string]shared.HostCapabilities, len(hostsInRequest))
	for _, hostCapability := range hostCapabilities {
		capabilityByHost[hostCapability.ComputeHost] = hostCapability
	}

	// Go through each host selector sequentially.
	for _, selector := range s.Scope.HostSelectors {
		selectedHosts := make(map[string]struct{})
		for _, host := range hostsInRequest {
			// Check if the host matches the selector.
			capability, ok := capabilityByHost[host]
			if !ok {
				// If the host does not have capabilities, skip it.
				continue
			}

			matches := false
			switch strings.ToLower(selector.Subject) {
			case "trait":
				matches = strings.Contains(capability.Traits, selector.Infix)
			case "hypervisortype":
				matches = strings.Contains(capability.HypervisorType, selector.Infix)
			default:
				// If the subject is not recognized, log an error and skip.
				traceLog.Error("scheduler: unknown host selector subject", "subject", selector.Subject)
				continue
			}

			if matches {
				// If the host matches the selector, add it to the in-scope map.
				selectedHosts[host] = struct{}{}
			}
		}

		// Apply the selected hosts to the in-scope map.
		switch strings.ToLower(selector.Operation) {
		case "union":
			// If the operation is union, simply add the selected hosts to the in-scope map.
			for host := range selectedHosts {
				hostsInScope[host] = struct{}{}
			}
		case "difference":
			// If the operation is difference, remove the selected hosts from the in-scope map.
			for host := range selectedHosts {
				delete(hostsInScope, host)
			}
		case "intersection":
			// If the operation is intersection, keep only the hosts that are both in the in-scope map and the selected hosts.
			for host := range hostsInScope {
				if _, ok := selectedHosts[host]; !ok {
					delete(hostsInScope, host)
				}
			}
		default:
			// If the operation is not recognized, log an error and skip.
			traceLog.Error("scheduler: unknown host selector operation", "operation", selector.Operation)
			continue
		}
	}

	// Check which hosts have been excluded from the scope.
	hostsNotInScope = make(map[string]struct{})
	for _, host := range request.GetHosts() {
		if _, ok := hostsInScope[host]; !ok {
			// If the host is not in scope, add it to the not in scope map.
			hostsNotInScope[host] = struct{}{}
		}
	}

	return hostsInScope, hostsNotInScope, nil
}

// Check if the spec is in scope based on the spec selectors.
// If there are no spec selectors, the spec is considered in scope.
func (s *StepScoper) isSpecInScope(traceLog *slog.Logger, request api.Request) bool {
	// If there is no scope, the spec is in scope.
	if len(s.Scope.SpecSelectors) == 0 {
		return true
	}
	for _, selector := range s.Scope.SpecSelectors {
		// Check if the selector matches the spec.
		matches := false
		if strings.EqualFold(selector.Subject, "flavor") {
			// Check if the flavor name contains the infix.
			matches = strings.Contains(request.GetSpec().Data.Flavor.Data.Name, selector.Infix)
		} else {
			// If the subject is not recognized, log an error and skip.
			traceLog.Error("scheduler: unknown spec selector subject", "subject", selector.Subject)
			continue
		}
		if strings.EqualFold(selector.Action, "skip") && matches {
			return false
		} else if strings.EqualFold(selector.Action, "continue") && matches {
			continue
		}
	}
	return true
}

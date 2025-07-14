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
)

type StepScoper struct {
	// The wrapped step to scope.
	Step scheduler.Step[api.ExternalSchedulerRequest]
	// The scope for this step.
	Scope conf.NovaSchedulerStepScope
	// The database to use for querying host capabilities.
	DB db.DB
}

// Get the name of the wrapped step.
func (s *StepScoper) GetName() string {
	return s.Step.GetName()
}

// Get the alias of the wrapped step.
func (s *StepScoper) GetAlias() string {
	return s.Step.GetAlias()
}

// Initialize the wrapped step with the database and options.
func (s *StepScoper) Init(alias string, db db.DB, opts conf.RawOpts) error {
	slog.Info("scheduler: init scope for step", "name", s.GetName())
	s.DB = db
	return s.Step.Init(alias, db, opts)
}

// Run the step and sRun(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error)
func (s *StepScoper) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	// If the spec is not in scope, skip it.
	if !s.isSpecInScope(traceLog, request) {
		return nil, scheduler.ErrStepSkipped
	}

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

	return result, nil
}

// Based on the provided host selectors, determine which hosts are in scope
// and which are not. The hosts in scope are returned in the first map,
// while the hosts not in scope are returned in the second map.
func (s *StepScoper) queryHostsInScope(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (
	hostsInScope map[string]struct{},
	hostsNotInScope map[string]struct{},
	err error,
) {

	// Initially, all hosts in the request are considered in scope.
	hostsInScope = make(map[string]struct{})
	for _, host := range request.Hosts {
		hostsInScope[host.ComputeHost] = struct{}{}
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
	capabilityByHost := make(map[string]shared.HostCapabilities, len(request.Hosts))
	for _, hostCapability := range hostCapabilities {
		capabilityByHost[hostCapability.ComputeHost] = hostCapability
	}

	// Go through each host selector sequentially.
	for _, selector := range s.Scope.HostSelectors {
		// Currently for host selectors we only support infix checking.
		if strings.ToLower(selector.Type) != "infix" {
			traceLog.Error("scheduler: unsupported host selector type", "type", selector.Type)
			continue
		}
		selectedHosts := make(map[string]struct{})
		for _, host := range request.Hosts {
			// Check if the host matches the selector.
			capability, ok := capabilityByHost[host.ComputeHost]
			if !ok {
				// If the host does not have capabilities, skip it.
				continue
			}
			matches := false
			cmp := strings.EqualFold
			switch {
			case cmp(selector.Subject, "trait") && cmp(selector.Type, "infix"):
				// Check if the trait contains the infix.
				matches = strings.Contains(capability.Traits, selector.Value.(string))
			case cmp(selector.Subject, "hypervisortype") && cmp(selector.Type, "infix"):
				// Check if the hypervisor type contains the infix.
				matches = strings.Contains(capability.HypervisorType, selector.Value.(string))
			default:
				// If the subject is not recognized, log an error and skip.
				traceLog.Error(
					"scheduler: unknown host selector",
					"subject", selector.Subject, "type", selector.Type,
				)
				continue
			}

			if matches {
				// If the host matches the selector, add it to the in-scope map.
				selectedHosts[host.ComputeHost] = struct{}{}
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
	for _, host := range request.Hosts {
		if _, ok := hostsInScope[host.ComputeHost]; !ok {
			// If the host is not in scope, add it to the not in scope map.
			hostsNotInScope[host.ComputeHost] = struct{}{}
		}
	}

	return hostsInScope, hostsNotInScope, nil
}

// Check if the spec is in scope based on the spec selectors.
// If there are no spec selectors, the spec is considered in scope.
func (s *StepScoper) isSpecInScope(traceLog *slog.Logger, request api.ExternalSchedulerRequest) bool {
	// If there is no scope, the spec is in scope.
	if len(s.Scope.SpecSelectors) == 0 {
		return true
	}
	for _, selector := range s.Scope.SpecSelectors {
		// Check if the selector matches the spec.
		matches := false
		cmp := strings.EqualFold
		switch {
		case cmp(selector.Subject, "flavor") && cmp(selector.Type, "infix"):
			// Check if the flavor name contains the infix.
			matches = strings.Contains(request.Spec.Data.Flavor.Data.Name, selector.Value.(string))
		case cmp(selector.Subject, "vmware") && cmp(selector.Type, "bool"):
			// Check if the VMware flag is set.
			matches = request.VMware == selector.Value.(bool)
		default:
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

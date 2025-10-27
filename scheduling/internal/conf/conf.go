// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

// Config for which validations to disable for a scheduler step.
type SchedulerStepDisabledValidationsConfig struct {
	// Whether to validate that no subjects are removed or added from the scheduler
	// step. This should only be disabled for scheduler steps that remove subjects.
	// Thus, if no value is provided, the default is false.
	SameSubjectNumberInOut bool `json:"sameSubjectNumberInOut,omitempty"`
	// Whether to validate that, after running the step, there are remaining subjects.
	// This should only be disabled for scheduler steps that are expected to
	// remove all subjects.
	SomeSubjectsRemain bool `json:"someSubjectsRemain,omitempty"`
}

// Scope that defines which hosts a scheduler step should be applied to.
// In addition, it also defines the traits for which the step should be applied.
type NovaSchedulerStepScope struct {
	// Selectors applied to the compute hosts.
	HostSelectors []NovaSchedulerStepHostSelector `json:"hostSelectors,omitempty"`
	// Selectors applied to the given nova spec.
	SpecSelectors []NovaSchedulerStepSpecSelector `json:"specSelectors,omitempty"`
}

type NovaSchedulerStepHostSelector struct {
	// One of: "trait", "hypervisorType"
	Subject string `json:"subject"`
	// Selector type, currently only "infix" is supported.
	Type string `json:"type,omitempty"`
	// Value of the selector (typed to the given type).
	Value any `json:"value,omitempty"`
	// How the selector should be applied:
	// Let A be the previous set of hosts, and B the scoped hosts.
	// - "union" means that the scoped hosts are added to the previous set of hosts.
	// - "difference" means that the scoped hosts are removed from the previous set of hosts.
	// - "intersection" means that the scoped hosts are the only ones that remain in the previous set of hosts.
	Operation string `json:"operation,omitempty"`
}

type NovaSchedulerStepSpecSelector struct {
	// One of: "flavor", "vmware"
	Subject string `json:"subject"`
	// Selector type: bool, infix.
	Type string `json:"type,omitempty"`
	// Value of the selector (typed to the given type).
	Value any `json:"value,omitempty"`
	// What to do if the selector is matched:
	// - "skip" means that the step is skipped.
	// - "continue" means that the step is applied.
	Action string `json:"action,omitempty"`
}

type NovaSchedulerStepHostCapabilities struct {
	// If given, the scheduler step will only be applied to hosts
	// that have ONE of the given traits.
	AnyOfTraitInfixes []string `json:"anyOfTraitInfixes,omitempty"`
	// If given, the scheduler step will only be applied to hosts
	// that have ONE of the given hypervisor types.
	AnyOfHypervisorTypeInfixes []string `json:"anyOfHypervisorTypeInfixes,omitempty"`
	// If given, the scheduler step will only be applied to hosts
	// that have ALL of the given traits.
	AllOfTraitInfixes []string `json:"allOfTraitInfixes,omitempty"`

	// If the selection should be inverted, i.e. the step should be applied to hosts
	// that do NOT match the aforementioned criteria.
	InvertSelection bool `json:"invertSelection,omitempty"`
}

func (s NovaSchedulerStepHostCapabilities) IsUndefined() bool {
	return len(s.AnyOfTraitInfixes) == 0 && len(s.AnyOfHypervisorTypeInfixes) == 0 && len(s.AllOfTraitInfixes) == 0
}

type NovaSchedulerStepSpecScope struct {
	// If given, the scheduler step will only be applied to specs
	// that contain ALL of the following infixes.
	AllOfFlavorNameInfixes []string `json:"allOfFlavorNameInfixes,omitempty"`
}

func (s NovaSchedulerStepSpecScope) IsUndefined() bool {
	return len(s.AllOfFlavorNameInfixes) == 0
}

type Config struct {
	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`

	// Lib modules configs.
	libconf.DBConfig `json:"db"`

	libconf.KeystoneConfig `json:"keystone"`
}

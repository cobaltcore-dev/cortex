// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

type SchedulerStepConfig[Extra any] struct {
	// The name of the step implementation.
	Name string `json:"name"`
	// The alias of this step, if any.
	//
	// The alias can be used to distinguish between different configurations
	// of the same step, or use a more specific name.
	Alias string `json:"alias,omitempty"`
	// Custom options for the step, as a raw yaml map.
	Options libconf.RawOpts `json:"options,omitempty"`
	// The validations to use for this step.
	DisabledValidations SchedulerStepDisabledValidationsConfig `json:"disabledValidations,omitempty"`

	// Additional configuration for the step, if needed.
	Extra *Extra `json:"extra,omitempty"`
}

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

// Configuration for the descheduler module.
type DeschedulerConfig struct {
	Nova NovaDeschedulerConfig `json:"nova"`
}

// Configuration for the nova descheduler.
type NovaDeschedulerConfig struct {
	// The availability of the nova service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
	// The steps to execute in the descheduler.
	Plugins []DeschedulerStepConfig `json:"plugins"`
	// If dry-run is disabled (by default its enabled).
	DisableDryRun bool `json:"disableDryRun,omitempty"`
}

type DeschedulerStepConfig struct {
	// The name of the step.
	Name string `json:"name"`
	// Custom options for the step, as a raw yaml map.
	Options libconf.RawOpts `json:"options,omitempty"`
}

type CinderSchedulerConfig struct {
	// Pipelines in this scheduler.
	Pipelines []CinderSchedulerPipelineConfig `json:"pipelines"`
}

type CinderSchedulerStepConfig = SchedulerStepConfig[struct{}]

type CinderSchedulerPipelineConfig struct {
	// Scheduler step plugins by their name.
	Plugins []CinderSchedulerStepConfig `json:"plugins"`

	// The name of this scheduler pipeline.
	// The name is used to distinguish and route between multiple pipelines.
	Name string `json:"name"`
}

type ManilaSchedulerConfig struct {
	// Pipelines in this scheduler.
	Pipelines []ManilaSchedulerPipelineConfig `json:"pipelines"`
}

type ManilaSchedulerStepConfig = SchedulerStepConfig[struct{}]

type ManilaSchedulerPipelineConfig struct {
	// Scheduler step plugins by their name.
	Plugins []ManilaSchedulerStepConfig `json:"plugins"`

	// The name of this scheduler pipeline.
	// The name is used to distinguish and route between multiple pipelines.
	Name string `json:"name"`
}

type MachineSchedulerConfig struct {
	// Pipelines in this scheduler.
	Pipelines []MachineSchedulerPipelineConfig `json:"pipelines"`
}

type MachineSchedulerStepConfig = SchedulerStepConfig[struct{}]

type MachineSchedulerPipelineConfig struct {
	// Scheduler step plugins by their name.
	Plugins []MachineSchedulerStepConfig `json:"plugins"`

	// The name of this scheduler pipeline.
	// The name is used to distinguish and route between multiple pipelines.
	Name string `json:"name"`
}

type NovaSchedulerConfig struct {
	// Pipelines in this scheduler.
	Pipelines []NovaSchedulerPipelineConfig `json:"pipelines"`
}

type NovaHypervisorType = string

const (
	NovaHypervisorTypeQEMU   NovaHypervisorType = "QEMU"
	NovaHypervisorTypeCH     NovaHypervisorType = "CH" // Cloud hypervisor
	NovaHypervisorTypeVMware NovaHypervisorType = "VMware vCenter Server"
	NovaHypervisorTypeIronic NovaHypervisorType = "ironic"
)

type NovaSchedulerLiquidAPIConfig struct {
	// Hypervisors that should be handled by the api.
	Hypervisors []NovaHypervisorType `json:"hypervisors"`
}

type NovaSchedulerStepExtraConfig struct {
	// The scope of the step, i.e. which hosts it should be applied to.
	Scope NovaSchedulerStepScope `json:"scope,omitempty"`
}

type NovaSchedulerStepConfig = SchedulerStepConfig[NovaSchedulerStepExtraConfig]

type NovaSchedulerPipelineConfig struct {
	// Scheduler step plugins by their name.
	Plugins []NovaSchedulerStepConfig `json:"plugins"`

	// The name of this scheduler pipeline.
	// The name is used to distinguish and route between multiple pipelines.
	Name string `json:"name"`
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

// Configuration for the scheduler module.
type SchedulerConfig struct {
	Nova     NovaSchedulerConfig    `json:"nova"`
	Manila   ManilaSchedulerConfig  `json:"manila"`
	Cinder   CinderSchedulerConfig  `json:"cinder"`
	Machines MachineSchedulerConfig `json:"machines"`
}

type Config struct {
	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`

	SchedulerConfig   `json:"scheduler"`
	DeschedulerConfig `json:"descheduler"`

	// Lib modules configs.
	libconf.DBConfig `json:"db"`

	libconf.KeystoneConfig `json:"keystone"`
}

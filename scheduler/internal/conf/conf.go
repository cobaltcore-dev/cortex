// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

type CinderSchedulerConfig struct {
	// Pipelines in this scheduler.
	Pipelines []CinderSchedulerPipelineConfig `json:"pipelines"`
}

type CinderSchedulerStepConfig = libconf.SchedulerStepConfig[struct{}]

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

type ManilaSchedulerStepConfig = libconf.SchedulerStepConfig[struct{}]

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

type MachineSchedulerStepConfig = libconf.SchedulerStepConfig[struct{}]

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
	// Configuration for the Liquid API.
	LiquidAPI NovaSchedulerLiquidAPIConfig `json:"liquidAPI"`
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

type NovaSchedulerStepConfig = libconf.SchedulerStepConfig[NovaSchedulerStepExtraConfig]

type NovaSchedulerPipelineConfig struct {
	// Scheduler step plugins by their name.
	Plugins []NovaSchedulerStepConfig `json:"plugins"`

	// The name of this scheduler pipeline.
	// The name is used to distinguish and route between multiple pipelines.
	Name string `json:"name"`

	// If all available hosts should be selected in the request,
	// regardless of what nova sends us in the request.
	// By default, this is false (use the hosts nova gives us).
	PreselectAllHosts bool `json:"preselectAllHosts"`
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
	Nova   NovaSchedulerConfig   `json:"nova"`
	Manila ManilaSchedulerConfig `json:"manila"`
	Cinder CinderSchedulerConfig `json:"cinder"`

	API SchedulerAPIConfig `json:"api"`
}

// Configuration for the scheduler API.
type SchedulerAPIConfig struct {
	// If request bodies should be logged out.
	// This feature is intended for debugging purposes only.
	LogRequestBodies bool `json:"logRequestBodies"`
}

// Configuration for the nova service.
type SyncOpenStackNovaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
}

// Configuration for the identity service.
type SyncOpenStackIdentityConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
}

// Configuration for the manila service.
type SyncOpenStackManilaConfig struct {
	// Availability of the service, such as "public", "internal", or "admin".
	Availability string `json:"availability"`
}

// Configuration for the sync/openstack module.
type SyncOpenStackConfig struct {
	// Configuration for the nova service.
	Nova SyncOpenStackNovaConfig `json:"nova"`
	// Configuration for the identity service.
	Identity SyncOpenStackIdentityConfig `json:"identity"`
	// Configuration for the manila service.
	Manila SyncOpenStackManilaConfig `json:"manila"`
}

// Configuration for the sync module.
type SyncConfig struct {
	OpenStack SyncOpenStackConfig `json:"openstack"`
}

type Config struct {
	SchedulerConfig `json:"scheduler"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`
	libconf.DBConfig         `json:"db"`
	libconf.MQTTConfig       `json:"mqtt"`

	// Required for e2e tests.
	libconf.KeystoneConfig `json:"keystone"`
	libconf.APIConfig      `json:"api"`
	SyncConfig             `json:"sync"`
}

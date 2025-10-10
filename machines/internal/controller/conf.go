// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

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

// Configuration for the scheduler module.
type SchedulerConfig struct {
	// Configuration for the machines scheduler pipeline (IronCore).
	Machines MachineSchedulerConfig `json:"machines"`
}

type Config struct {
	SchedulerConfig `json:"scheduler"`

	// Config needed by the library scheduler pipeline.
	MonitoringConfig libconf.MonitoringConfig `json:"monitoring"`
	MQTTConfig       libconf.MQTTConfig       `json:"mqtt"`
	DBConfig         libconf.DBConfig         `json:"db"`
}

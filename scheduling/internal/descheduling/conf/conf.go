// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

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

type Config struct {
	DeschedulerConfig `json:"descheduler"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`
	libconf.DBConfig         `json:"db"`

	// Generally needed to expose an /up endpoint.
	libconf.APIConfig `json:"api"`
	// Needed to connect to OpenStack.
	libconf.KeystoneConfig `json:"keystone"`
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/internal/conf"
)

// Configuration for the kpis module.
type KPIsConfig struct {
	// KPI plugins to use.
	Plugins []KPIPluginConfig `json:"plugins"`
}

// Configuration for a single KPI plugin.
type KPIPluginConfig struct {
	// The name of the KPI plugin.
	Name string `json:"name"`
	// Custom options for the KPI plugin, as a raw json map.
	Options libconf.RawOpts `json:"options,omitempty"`
	// The dependencies this KPI plugin needs.
	libconf.DependencyConfig `json:"dependencies,omitempty"`
}

type Config struct {
	KPIsConfig `json:"kpis"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`
	libconf.DBConfig         `json:"db"`

	// Generally needed to expose an /up endpoint.
	libconf.APIConfig `json:"api"`
}

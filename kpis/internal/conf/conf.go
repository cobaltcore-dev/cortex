// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
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
}

type Config struct {
	KPIsConfig `json:"kpis"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.DBConfig         `json:"db"`
}

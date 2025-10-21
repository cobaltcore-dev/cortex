// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

type DependencyConfig struct {
	Datasources []string `json:"datasources,omitempty"`
	Extractors  []string `json:"extractors,omitempty"`
}

type FeatureExtractorConfig struct {
	// The name of the extractor.
	Name string `json:"name"`
	// Custom options for the extractor, as a raw yaml map.
	Options libconf.RawOpts `json:"options,omitempty"`
	// The dependencies this extractor needs.
	DependencyConfig `json:"dependencies,omitempty"`
	// Recency that tells how old a feature needs to be to be recalculated
	RecencySeconds *int `json:"recencySeconds,omitempty"`
	// MQTT topic to publish the features to.
	// If not set, the extractor will not publish features to MQTT.
	MQTTTopic string `json:"mqttTopic,omitempty"`
}

// Configuration for the features module.
type ExtractorConfig struct {
	Plugins []FeatureExtractorConfig `json:"plugins"`
}

type Config struct {
	ExtractorConfig `json:"extractor"`

	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
	libconf.LoggingConfig    `json:"logging"`

	// Generally needed to expose an /up endpoint.
	libconf.APIConfig `json:"api"`
}

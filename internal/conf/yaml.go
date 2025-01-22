// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

// Metric configuration for the sync/prometheus module.
type SyncPrometheusMetricConfig struct {
	Name              string `yaml:"name"`
	Type              string `yaml:"type"`
	TimeRangeSeconds  *int   `yaml:"timeRangeSeconds,omitempty"`
	IntervalSeconds   *int   `yaml:"intervalSeconds,omitempty"`
	ResolutionSeconds *int   `yaml:"resolutionSeconds,omitempty"`
}

// Configuration for the sync/prometheus module containing a list of metrics.
type SyncPrometheusConfig struct {
	Metrics []SyncPrometheusMetricConfig `yaml:"metrics,omitempty"`
}

// Configuration for the sync/openstack module.
type SyncOpenStackConfig struct {
	HypervisorsEnabled *bool `yaml:"hypervisors"`
	ServersEnabled     *bool `yaml:"servers"`
}

// Configuration for the sync module.
type SyncConfig struct {
	Prometheus SyncPrometheusConfig `yaml:"prometheus"`
	OpenStack  SyncOpenStackConfig  `yaml:"openstack"`
}

// Configuration for the features module.
type FeaturesConfig struct {
	Extractors []struct {
		Name string `yaml:"name"`
		// The dependencies this extractor needs.
		DependencyConfig `yaml:"dependencies,omitempty"`
	} `yaml:"extractors"`
}

// Configuration for the scheduler module.
type SchedulerConfig struct {
	Steps []struct {
		Name    string         `yaml:"name"`
		Options map[string]any `yaml:"options"`
		// The dependencies this step needs.
		DependencyConfig `yaml:"dependencies,omitempty"`
	} `yaml:"steps"`
}

// Configuration for the cortex service.
type Config interface {
	GetSyncConfig() SyncConfig
	GetFeaturesConfig() FeaturesConfig
	GetSchedulerConfig() SchedulerConfig
	// Check if the configuration is valid.
	Validate() error
}

type config struct {
	SyncConfig      `yaml:"sync"`
	FeaturesConfig  `yaml:"features"`
	SchedulerConfig `yaml:"scheduler"`
}

// Create a new configuration from the default config yaml file.
func NewConfig() Config {
	return newConfigFromFile("/etc/config/conf.yaml")
}

// Create a new configuration from the given file.
func newConfigFromFile(filepath string) Config {
	file, err := os.Open(filepath)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	return newConfigFromBytes(bytes)
}

// Create a new configuration from the given bytes.
func newConfigFromBytes(bytes []byte) Config {
	var c config
	if err := yaml.Unmarshal(bytes, &c); err != nil {
		panic(err)
	}
	return &c
}

func (c *config) GetSyncConfig() SyncConfig           { return c.SyncConfig }
func (c *config) GetFeaturesConfig() FeaturesConfig   { return c.FeaturesConfig }
func (c *config) GetSchedulerConfig() SchedulerConfig { return c.SchedulerConfig }

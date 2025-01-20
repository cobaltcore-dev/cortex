// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

type SyncPrometheusMetricConfig struct {
	Name              string `yaml:"name"`
	Type              string `yaml:"type"`
	TimeRangeSeconds  int    `yaml:"timeRangeSeconds"`
	IntervalSeconds   int    `yaml:"intervalSeconds"`
	ResolutionSeconds int    `yaml:"resolutionSeconds"`
}

type SyncPrometheusConfig struct {
	Metrics []SyncPrometheusMetricConfig `yaml:"metrics"`
}

type SyncOpenStackConfig struct {
	HypervisorsEnabled bool `yaml:"hypervisors"`
	ServersEnabled     bool `yaml:"servers"`
}

type SyncConfig struct {
	Prometheus SyncPrometheusConfig `yaml:"prometheus"`
	OpenStack  SyncOpenStackConfig  `yaml:"openstack"`
}

type FeaturesConfig struct {
	Extractors []struct {
		Name string `yaml:"name"`
	} `yaml:"extractors"`
}

type SchedulerConfig struct {
	Steps []struct {
		Name    string         `yaml:"name"`
		Options map[string]any `yaml:"options"`
	} `yaml:"steps"`
}

type Config interface {
	GetSyncConfig() SyncConfig
	GetFeaturesConfig() FeaturesConfig
	GetSchedulerConfig() SchedulerConfig
}

type config struct {
	SyncConfig      `yaml:"sync"`
	FeaturesConfig  `yaml:"features"`
	SchedulerConfig `yaml:"scheduler"`
}

func NewConfig() Config {
	return newConfigFromFile("/etc/config/conf.yaml")
}

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
	var c config
	if err := yaml.Unmarshal(bytes, &c); err != nil {
		panic(err)
	}
	return &c
}

func (c *config) GetSyncConfig() SyncConfig           { return c.SyncConfig }
func (c *config) GetFeaturesConfig() FeaturesConfig   { return c.FeaturesConfig }
func (c *config) GetSchedulerConfig() SchedulerConfig { return c.SchedulerConfig }

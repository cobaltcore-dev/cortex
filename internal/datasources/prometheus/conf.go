// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

type PrometheusConfig interface {
	GetMetricsToSync() []MetricConfig
}

type MetricConfig struct {
	Name              string `yaml:"name"`
	Type              string `yaml:"type"`
	TimeRangeSeconds  int    `yaml:"timeRangeSeconds"`
	IntervalSeconds   int    `yaml:"intervalSeconds"`
	ResolutionSeconds int    `yaml:"resolutionSeconds"`
}

type prometheusConfig struct {
	Sync struct {
		Metrics []MetricConfig `yaml:"metrics"`
	} `yaml:"sync"`
}

func NewPrometheusConfig() PrometheusConfig {
	file, err := os.Open("/etc/config/conf.yaml")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	var config prometheusConfig
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		panic(err)
	}
	return &config
}

func (c *prometheusConfig) GetMetricsToSync() []MetricConfig {
	return c.Sync.Metrics
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// Configuration that is passed in the config file to specify dependencies.
type DependencyConfig struct {
	Sync struct {
		OpenStack struct {
			HypervisorsEnabled *bool `yaml:"hypervisors,omitempty"`
			ServersEnabled     *bool `yaml:"servers,omitempty"`
		} `yaml:"openstack,omitempty"`
		Prometheus struct {
			MetricNames []string `yaml:"metrics,omitempty"`
		} `yaml:"prometheus,omitempty"`
	}
	Features struct {
		ExtractorNames []string `yaml:"extractors,omitempty"`
	}
}

// Validate if the dependencies are satisfied in the given config.
func (deps *DependencyConfig) validate(c config) error {
	hyperNeeded := deps.Sync.OpenStack.HypervisorsEnabled
	hyperProvided := c.OpenStack.HypervisorsEnabled
	if hyperNeeded != nil && (hyperProvided == nil || *hyperNeeded != *hyperProvided) {
		return errors.New("OpenStack hypervisorsEnabled dependency not satisfied")
	}
	serversNeeded := deps.Sync.OpenStack.ServersEnabled
	serversProvided := c.OpenStack.ServersEnabled
	if serversNeeded != nil && (serversProvided == nil || *serversNeeded != *serversProvided) {
		fmt.Printf("serversNeeded: %v, serversProvided: %v\n", serversNeeded, serversProvided)
		return errors.New("OpenStack serversEnabled dependency not satisfied")
	}
	confedMetrics := make(map[string]bool)
	for _, metric := range c.SyncConfig.Prometheus.Metrics {
		confedMetrics[metric.Name] = true
	}
	for _, metric := range deps.Sync.Prometheus.MetricNames {
		if !confedMetrics[metric] {
			return fmt.Errorf("prometheus metric dependency %s not satisfied", metric)
		}
	}
	confedExtractors := make(map[string]bool)
	for _, extractor := range c.FeaturesConfig.Extractors {
		confedExtractors[extractor.Name] = true
	}
	for _, extractor := range deps.Features.ExtractorNames {
		if !confedExtractors[extractor] {
			return fmt.Errorf("feature extractor dependency %s not satisfied", extractor)
		}
	}
	return nil
}

// Check if all dependencies are satisfied.
func (c *config) Validate() error {
	for _, extractor := range c.FeaturesConfig.Extractors {
		if err := extractor.DependencyConfig.validate(*c); err != nil {
			return err
		}
	}
	for _, step := range c.SchedulerConfig.Steps {
		if err := step.DependencyConfig.validate(*c); err != nil {
			return err
		}
	}
	if c.SchedulerConfig.LogRequestBodies {
		logging.Log.Warn("logging request bodies is enabled (debug feature)")
	}
	return nil
}

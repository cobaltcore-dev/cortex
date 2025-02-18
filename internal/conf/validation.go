// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// Configuration that is passed in the config file to specify dependencies.
type DependencyConfig struct {
	Sync struct {
		OpenStack struct {
			Nova struct {
				ObjectTypes []string `yaml:"types,omitempty"`
			} `yaml:"nova,omitempty"`
			Placement struct {
				ObjectTypes []string `yaml:"types,omitempty"`
			} `yaml:"placement,omitempty"`
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
	confedNovaObjects := make(map[string]bool)
	for _, objectType := range c.SyncConfig.OpenStack.Nova.Types {
		confedNovaObjects[objectType] = true
	}
	for _, objectType := range deps.Sync.OpenStack.Nova.ObjectTypes {
		if !confedNovaObjects[objectType] {
			return fmt.Errorf("openstack object type dependency %s not satisfied", objectType)
		}
	}
	confedPlacementObjects := make(map[string]bool)
	for _, objectType := range c.SyncConfig.OpenStack.Placement.Types {
		confedPlacementObjects[objectType] = true
	}
	for _, objectType := range deps.Sync.OpenStack.Placement.ObjectTypes {
		if !confedPlacementObjects[objectType] {
			return fmt.Errorf("openstack object type dependency %s not satisfied", objectType)
		}
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
		slog.Warn("logging request bodies is enabled (debug feature)")
	}
	// If traits (placement) are specified, the resource providers must be synced as well.
	if len(c.SyncConfig.OpenStack.Placement.Types) > 0 {
		if !slices.Contains(c.SyncConfig.OpenStack.Placement.Types, "resource_providers") {
			return errors.New("resource_providers must be synced if traits are specified")
		}
	}
	// Check the keystone URL.
	if c.SyncConfig.OpenStack.Keystone.URL != "" && !strings.Contains(c.SyncConfig.OpenStack.Keystone.URL, "/v3") {
		return fmt.Errorf(
			"expected v3 Keystone URL, but got %s",
			c.SyncConfig.OpenStack.Keystone.URL,
		)
	}
	// OpenStack urls should end without a slash.
	for _, url := range []string{
		c.SyncConfig.OpenStack.Keystone.URL,
		c.SyncConfig.OpenStack.Nova.URL,
		c.SyncConfig.OpenStack.Placement.URL,
	} {
		if strings.HasSuffix(url, "/") {
			return fmt.Errorf("openstack url %s should not end with a slash", url)
		}
	}
	return nil
}

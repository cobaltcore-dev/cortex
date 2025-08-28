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
				ObjectTypes []string `json:"types,omitempty"`
			} `json:"nova,omitempty"`
			Placement struct {
				ObjectTypes []string `json:"types,omitempty"`
			} `json:"placement,omitempty"`
		} `json:"openstack,omitempty"`
		Prometheus struct {
			Metrics []struct {
				Alias string `json:"alias,omitempty"`
				Type  string `json:"type,omitempty"`
			} `json:"metrics,omitempty"`
		} `json:"prometheus,omitempty"`
	}
	Extractors []string `json:"extractors,omitempty"`
}

// Validate if the dependencies are satisfied in the given config.
func (deps *DependencyConfig) validate(c SharedConfig) error {
	confedNovaObjects := make(map[string]bool)
	for _, objectType := range c.OpenStack.Nova.Types {
		confedNovaObjects[objectType] = true
	}
	for _, objectType := range deps.Sync.OpenStack.Nova.ObjectTypes {
		if !confedNovaObjects[objectType] {
			return fmt.Errorf(
				"openstack object type dependency %s not satisfied, got %v",
				objectType, c.OpenStack.Nova.Types,
			)
		}
	}
	confedPlacementObjects := make(map[string]bool)
	for _, objectType := range c.OpenStack.Placement.Types {
		confedPlacementObjects[objectType] = true
	}
	for _, objectType := range deps.Sync.OpenStack.Placement.ObjectTypes {
		if !confedPlacementObjects[objectType] {
			return fmt.Errorf(
				"openstack object type dependency %s not satisfied, got %v",
				objectType, c.OpenStack.Placement.Types,
			)
		}
	}
	confedMetricAliases := make(map[string]bool)
	confedMetricTypes := make(map[string]bool)
	for _, metric := range c.Prometheus.Metrics {
		confedMetricAliases[metric.Alias] = true
		confedMetricTypes[metric.Type] = true
	}
	for _, metric := range deps.Sync.Prometheus.Metrics {
		if !confedMetricAliases[metric.Alias] && !confedMetricTypes[metric.Type] {
			return fmt.Errorf(
				"prometheus metric dependency %s not satisfied, got %v",
				metric, c.Prometheus.Metrics,
			)
		}
	}
	confedExtractors := make(map[string]bool)
	for _, extractor := range c.ExtractorConfig.Plugins {
		confedExtractors[extractor.Name] = true
	}
	for _, extractor := range deps.Extractors {
		if !confedExtractors[extractor] {
			return fmt.Errorf(
				"feature extractor dependency %s not satisfied, got %v",
				extractor, c.ExtractorConfig.Plugins,
			)
		}
	}
	return nil
}

// Check if all dependencies are satisfied.
func (c *SharedConfig) Validate() error {
	for _, extractor := range c.ExtractorConfig.Plugins {
		if err := extractor.validate(*c); err != nil {
			return err
		}
	}
	for _, kpi := range c.KPIsConfig.Plugins {
		if err := kpi.validate(*c); err != nil {
			return err
		}
	}
	for _, step := range c.SchedulerConfig.Nova.Plugins {
		if err := step.validate(*c); err != nil {
			return err
		}
	}
	for _, step := range c.DeschedulerConfig.Nova.Plugins {
		if err := step.validate(*c); err != nil {
			return err
		}
	}
	// Check general dependencies needed by all scheduler steps.
	if err := c.SchedulerConfig.Nova.validate(*c); err != nil {
		return err
	}
	if c.API.LogRequestBodies {
		slog.Warn("logging request bodies is enabled (debug feature)")
	}
	// If traits (placement) are specified, the resource providers must be synced as well.
	if len(c.OpenStack.Placement.Types) > 0 {
		if !slices.Contains(c.OpenStack.Placement.Types, "resource_providers") {
			return errors.New("resource_providers must be synced if dependent models are specified")
		}
	}
	// Check the keystone URL.
	if c.KeystoneConfig.URL != "" && !strings.Contains(c.KeystoneConfig.URL, "/v3") {
		return fmt.Errorf(
			"expected v3 Keystone URL, but got %s",
			c.KeystoneConfig.URL,
		)
	}
	// OpenStack urls should end without a slash.
	for _, url := range []string{
		c.KeystoneConfig.URL,
	} {
		if strings.HasSuffix(url, "/") {
			return fmt.Errorf("openstack url %s should not end with a slash", url)
		}
	}
	// Check that the service availability is valid.
	validAvailabilities := []string{"public", "internal", "admin"}
	if c.OpenStack.Nova.Availability == "" {
		c.OpenStack.Nova.Availability = "public"
	}
	if c.OpenStack.Placement.Availability == "" {
		c.OpenStack.Placement.Availability = "public"
	}
	if c.OpenStack.Cinder.Availability == "" {
		c.OpenStack.Cinder.Availability = "public"
	}
	if !slices.Contains(validAvailabilities, c.OpenStack.Nova.Availability) {
		return fmt.Errorf("invalid nova availability %s", c.OpenStack.Nova.Availability)
	}
	if !slices.Contains(validAvailabilities, c.OpenStack.Placement.Availability) {
		return fmt.Errorf("invalid placement availability %s", c.OpenStack.Placement.Availability)
	}
	if !slices.Contains(validAvailabilities, c.OpenStack.Cinder.Availability) {
		return fmt.Errorf("invalid cinder availability %s", c.OpenStack.Cinder.Availability)
	}

	// Check that all confed metric types have a host to sync from.
	confedMetricTypes := make(map[string]bool)
	for _, metric := range c.Prometheus.Metrics {
		confedMetricTypes[metric.Type] = true
	}
	providedMetricTypes := make(map[string]bool)
	for _, host := range c.Prometheus.Hosts {
		for _, metricType := range host.ProvidedMetricTypes {
			providedMetricTypes[metricType] = true
		}
	}
	for metricType := range confedMetricTypes {
		if !providedMetricTypes[metricType] {
			return fmt.Errorf("no host provided for metric type %s", metricType)
		}
	}
	return nil
}

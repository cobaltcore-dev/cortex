// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"fmt"
	"strings"
)

// Configuration that is passed in the config file to specify dependencies.
// TODO This should be part of the knowledge config because it is only relevant for execution order.
type DependencyConfig struct {
	Datasources struct {
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
	} `json:"datasources,omitempty"`
	Extractors []string `json:"extractors,omitempty"`
}

// Check if all dependencies are satisfied.
func (c *SharedConfig) Validate() error {
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
	return nil
}

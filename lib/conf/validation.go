// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"fmt"
	"strings"
)

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

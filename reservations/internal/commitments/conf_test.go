// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/conf"
)

func TestConfig_Structure(t *testing.T) {
	// Test that Config struct can be instantiated
	config := Config{
		Keystone: conf.KeystoneConfig{
			URL:                 "http://keystone:5000/v3",
			OSUsername:          "test-user",
			OSPassword:          "test-password",
			OSProjectName:       "test-project",
			OSUserDomainName:    "default",
			OSProjectDomainName: "default",
		},
	}

	if config.Keystone.URL != "http://keystone:5000/v3" {
		t.Errorf("Expected Keystone URL to be 'http://keystone:5000/v3', got %v", config.Keystone.URL)
	}
}

func TestConfig_EmptyValues(t *testing.T) {
	// Test that Config struct works with empty values
	config := Config{}

	if config.Keystone.URL != "" {
		t.Errorf("Expected empty Keystone URL, got %v", config.Keystone.URL)
	}
}

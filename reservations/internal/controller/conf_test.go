// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"
)

func TestConfig_Structure(t *testing.T) {
	// Test that Config struct can be instantiated
	config := Config{
		Endpoints: EndpointsConfig{
			NovaExternalScheduler: "http://localhost:8080",
		},
		Hypervisors: []string{"kvm", "vmware"},
	}

	// Verify the config fields are set correctly
	if config.Endpoints.NovaExternalScheduler != "http://localhost:8080" {
		t.Errorf("Expected NovaExternalScheduler to be 'http://localhost:8080', got %v", config.Endpoints.NovaExternalScheduler)
	}

	if len(config.Hypervisors) != 2 {
		t.Errorf("Expected 2 hypervisors, got %d", len(config.Hypervisors))
	}

	if config.Hypervisors[0] != "kvm" {
		t.Errorf("Expected first hypervisor to be 'kvm', got %v", config.Hypervisors[0])
	}

	if config.Hypervisors[1] != "vmware" {
		t.Errorf("Expected second hypervisor to be 'vmware', got %v", config.Hypervisors[1])
	}
}

func TestEndpointsConfig_Structure(t *testing.T) {
	// Test that EndpointsConfig struct can be instantiated
	endpoints := EndpointsConfig{
		NovaExternalScheduler: "http://nova-scheduler:8080/v1/schedule",
	}

	if endpoints.NovaExternalScheduler != "http://nova-scheduler:8080/v1/schedule" {
		t.Errorf("Expected NovaExternalScheduler to be 'http://nova-scheduler:8080/v1/schedule', got %v", endpoints.NovaExternalScheduler)
	}
}

func TestConfig_EmptyValues(t *testing.T) {
	// Test that Config struct works with empty values
	config := Config{}

	if config.Endpoints.NovaExternalScheduler != "" {
		t.Errorf("Expected empty NovaExternalScheduler, got %v", config.Endpoints.NovaExternalScheduler)
	}

	if len(config.Hypervisors) != 0 {
		t.Errorf("Expected 0 hypervisors, got %d", len(config.Hypervisors))
	}
}

func TestConfig_HypervisorsList(t *testing.T) {
	// Test various hypervisor configurations
	tests := []struct {
		name         string
		hypervisors  []string
		expectedLen  int
		expectedVals []string
	}{
		{
			name:         "single hypervisor",
			hypervisors:  []string{"kvm"},
			expectedLen:  1,
			expectedVals: []string{"kvm"},
		},
		{
			name:         "multiple hypervisors",
			hypervisors:  []string{"kvm", "vmware", "xen"},
			expectedLen:  3,
			expectedVals: []string{"kvm", "vmware", "xen"},
		},
		{
			name:         "empty hypervisors",
			hypervisors:  []string{},
			expectedLen:  0,
			expectedVals: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				Hypervisors: tt.hypervisors,
			}

			if len(config.Hypervisors) != tt.expectedLen {
				t.Errorf("Expected %d hypervisors, got %d", tt.expectedLen, len(config.Hypervisors))
			}

			for i, expected := range tt.expectedVals {
				if i >= len(config.Hypervisors) {
					t.Errorf("Expected hypervisor at index %d to be %v, but index is out of range", i, expected)
					continue
				}
				if config.Hypervisors[i] != expected {
					t.Errorf("Expected hypervisor at index %d to be %v, got %v", i, expected, config.Hypervisors[i])
				}
			}
		})
	}
}

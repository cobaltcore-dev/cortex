// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

// Endpoints for the reservations operator.
type EndpointsConfig struct {
	// The nova external scheduler endpoint.
	NovaExternalScheduler string `json:"novaExternalScheduler"`
}

// Configuration for the reservations operator.
type Config struct {
	// The endpoint where to find the nova external scheduler endpoint.
	Endpoints EndpointsConfig `json:"endpoints"`
	// Hypervisor types that should be managed.
	Hypervisors []string `json:"hypervisors"`
}

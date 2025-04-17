// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

type Pipeline interface {
	// Run the scheduling pipeline with the given request.
	Run(request Request) ([]string, error)
}

// Request to the Cortex scheduling pipeline.
type Request interface {
	// Specification of the scheduling request.
	GetSpec() NovaObject[NovaSpec]
	// Request context from Nova that contains additional meta information.
	GetContext() NovaRequestContext
	// Whether the Nova scheduling request is a rebuild request.
	GetRebuild() bool
	// Whether the Nova scheduling request is a resize request.
	GetResize() bool
	// Whether the Nova scheduling request is a live migration.
	GetLive() bool
	// Whether the affected VM is a VMware VM.
	GetVMware() bool
	// List of hosts to be considered for scheduling.
	// If the list is nil, all hosts are considered.
	GetHosts() []string
	// Map of weights to start with.
	// If the map is nil, all hosts will have the default weight starting.
	GetWeights() map[string]float64
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/manila"

type Pipeline interface {
	// Run the scheduling pipeline with the given request.
	Run(request Request) ([]string, error)
}

// Request to the Cortex scheduling pipeline.
type Request interface {
	// Specification of the scheduling request.
	GetSpec() any
	// Request context from Manila that contains additional meta information.
	GetContext() api.ManilaRequestContext
	// List of hosts to be considered for scheduling.
	// If the list is nil, all hosts are considered.
	GetHosts() []string
	// Map of weights to start with.
	// If the map is nil, all hosts will have the default weight starting.
	GetWeights() map[string]float64
}

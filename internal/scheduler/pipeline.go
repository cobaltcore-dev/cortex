// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"
)

// State passed through the pipeline.
// Each step in the pipeline can modify the hosts or their weights.
type pipelineState struct {
	Spec struct {
		ProjectID string
	}
	Hosts []struct {
		Name   string
		Status string
	}
	Weights map[string]float64
}

// Pipeline steps that are executed in order.
var pipeline = []func(*pipelineState) error{
	antiAffinityNoisyProjects,
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func evaluatePipeline(state pipelineState) ([]string, error) {
	stateRef := &state // Pass a reference for in-place modification.
	for _, step := range pipeline {
		if err := step(stateRef); err != nil {
			return nil, err
		}
	}
	// Order the list of hosts by their weights.
	sort.Slice(state.Hosts, func(i, j int) bool {
		return state.Weights[state.Hosts[i].Name] > state.Weights[state.Hosts[j].Name]
	})
	// Flatten to a list of host names.
	hostNames := make([]string, len(state.Hosts))
	for i, host := range state.Hosts {
		hostNames[i] = host.Name
	}
	return hostNames, nil
}

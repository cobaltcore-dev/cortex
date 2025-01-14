// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"
)

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

var steps = []func(pipelineState) (pipelineState, error){
	antiAffinityNoisyProjects,
}

func evaluatePipeline(state pipelineState) ([]string, error) {
	for _, step := range steps {
		var err error
		state, err = step(state)
		if err != nil {
			return nil, err
		}
	}

	// Order the list of hosts by their weights
	sort.Slice(state.Hosts, func(i, j int) bool {
		return state.Weights[state.Hosts[i].Name] > state.Weights[state.Hosts[j].Name]
	})

	hostNames := make([]string, len(state.Hosts))
	for i, host := range state.Hosts {
		hostNames[i] = host.Name
	}
	return hostNames, nil
}

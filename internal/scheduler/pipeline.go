// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/db"
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

type PipelineStep interface {
	Run(state *pipelineState) error
}

type Pipeline interface {
	Run(state *pipelineState) ([]string, error)
}

type pipeline struct {
	Steps []PipelineStep
}

func NewPipeline(db db.DB) Pipeline {
	return &pipeline{
		Steps: []PipelineStep{
			NewAntiAffinityNoisyProjectsStep(db),
		},
	}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(state *pipelineState) ([]string, error) {
	for _, step := range p.Steps {
		if err := step.Run(state); err != nil {
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

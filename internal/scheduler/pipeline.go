// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"
)

type pipelineContext struct {
	Spec struct {
		ProjectId string
	}
	Hosts []struct {
		Name   string
		Status string
	}
	Weights map[string]float64
}

var steps = []func(pipelineContext) (pipelineContext, error){
	antiAffinityNoisyProjects,
}

func evaluatePipeline(ctx pipelineContext) ([]string, error) {
	for _, step := range steps {
		var err error
		ctx, err = step(ctx)
		if err != nil {
			return nil, err
		}
	}

	// Order the list of hosts by their weights
	sort.Slice(ctx.Hosts, func(i, j int) bool {
		return ctx.Weights[ctx.Hosts[i].Name] > ctx.Weights[ctx.Hosts[j].Name]
	})

	hostNames := make([]string, len(ctx.Hosts))
	for i, host := range ctx.Hosts {
		hostNames[i] = host.Name
	}
	return hostNames, nil
}

func antiAffinityNoisyProjects(ctx pipelineContext) (pipelineContext, error) {
	// TODO:
	// - Get pairs of (noisy project, host) from the database.
	// - Check if we're spawning a VM for a noisy project.
	// - Downvote the hosts this project is currently running on.
	return ctx, nil
}

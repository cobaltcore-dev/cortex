// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

type antiAffinityNoisyProjectsStep struct{}

func NewAntiAffinityNoisyProjectsStep() PipelineStep {
	return &antiAffinityNoisyProjectsStep{}
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *antiAffinityNoisyProjectsStep) Run(state *pipelineState) error {
	logging.Log.Info("scheduler: anti-affinity - noisy projects")

	// If the average CPU usage is above this threshold, the project is considered noisy.
	const avgCPUThreshold float64 = 20.0
	var noisyProjects []features.ProjectNoisiness
	if err := db.Get().Model(&noisyProjects).
		Where("avg_cpu_of_project > ?", avgCPUThreshold).
		Select(); err != nil {
		return err
	}

	// Get the hosts we need to push the VM away from.
	var hostsByProject = make(map[string][]string)
	for _, p := range noisyProjects {
		hostsByProject[p.Project] = append(hostsByProject[p.Project], p.Host)
	}
	val, ok := hostsByProject[state.Spec.ProjectID]
	if !ok {
		// No noisy project, nothing to do.
		return nil
	}
	// Downvote the hosts this project is currently running on.
	for i := range state.Hosts {
		for _, host := range val {
			if state.Hosts[i].Name == host {
				state.Weights[state.Hosts[i].Name] = 0.0
				logging.Log.Info("scheduler: downvoting host", "host", host, "project", state.Spec.ProjectID)
			}
		}
	}
	return nil
}

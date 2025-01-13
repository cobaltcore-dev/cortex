// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

func antiAffinityNoisyProjects(state pipelineState) (pipelineState, error) {
	logging.Log.Info("scheduler: anti-affinity - noisy projects")

	// If the average CPU usage is above this threshold, the project is considered noisy.
	const avgCpuThreshold float64 = 20.0
	var noisyProjects []features.ProjectNoisiness
	if err := db.DB.Model(&noisyProjects).
		Where("avg_cpu_on_host > ?", avgCpuThreshold).
		Select(); err != nil {
		return state, err
	}

	// Get the hosts we need to push the VM away from.
	var hostsByProject = make(map[string][]string)
	for _, p := range noisyProjects {
		hostsByProject[p.Project] = append(hostsByProject[p.Project], p.Host)
	}
	val, ok := hostsByProject[state.Spec.ProjectId]
	if !ok {
		// No noisy project, nothing to do.
		return state, nil
	}
	// Downvote the hosts this project is currently running on.
	for i := range state.Hosts {
		for _, host := range val {
			if state.Hosts[i].Name == host {
				state.Weights[state.Hosts[i].Name] = 0.0
				logging.Log.Info("scheduler: downvoting host", "host", host, "project", state.Spec.ProjectId)
			}
		}
	}
	return state, nil
}

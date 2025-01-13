// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
)

func antiAffinityNoisyProjects(state pipelineState) (pipelineState, error) {
	log.Println("Scheduler: running anti-affinity for noisy projects")
	// Get pairs of (noisy project, host) from the database.
	var noisyProjects []features.NoisyProject
	if err := db.DB.Model(&noisyProjects).Select(); err != nil {
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
				log.Printf("Scheduler: downvoting host %s for project %s\n", host, state.Spec.ProjectId)
			}
		}
	}
	return state, nil
}

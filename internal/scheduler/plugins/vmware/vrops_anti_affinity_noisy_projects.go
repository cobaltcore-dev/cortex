// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

type VROpsAntiAffinityNoisyProjectsStep struct {
	DB              db.DB
	AvgCPUThreshold float64
	ActivationOnHit float64
}

func (s *VROpsAntiAffinityNoisyProjectsStep) GetName() string {
	return "vrops_anti_affinity_noisy_projects"
}

func (s *VROpsAntiAffinityNoisyProjectsStep) Init(db db.DB, opts map[string]any) error {
	s.DB = db

	avgCPUThreshold, ok := opts["avgCPUThreshold"]
	if !ok {
		return errors.New("missing avgCPUThreshold")
	}
	if avgCPUThresholdInt, okInt := avgCPUThreshold.(int); okInt {
		avgCPUThreshold = float64(avgCPUThresholdInt)
	}
	s.AvgCPUThreshold = avgCPUThreshold.(float64)

	activationOnHit, ok := opts["activationOnHit"]
	if !ok {
		return errors.New("missing activationOnHit")
	}
	if activationOnHitInt, okInt := activationOnHit.(int); okInt {
		activationOnHit = float64(activationOnHitInt)
	}
	s.ActivationOnHit = activationOnHit.(float64)

	return nil
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *VROpsAntiAffinityNoisyProjectsStep) Run(state *plugins.State) error {
	// If the average CPU usage is above the threshold, the project is considered noisy.
	var noisyProjects []vmware.VROpsProjectNoisiness
	if err := s.DB.Get().Model(&noisyProjects).
		Where("avg_cpu_of_project > ?", s.AvgCPUThreshold).
		Where("project = ?", state.Spec.ProjectID).
		Select(); err != nil {
		return err
	}

	// Get the hosts we need to push the VM away from.
	var hostsByProject = make(map[string][]string)
	for _, p := range noisyProjects {
		hostsByProject[p.Project] = append(hostsByProject[p.Project], p.ComputeHost)
	}
	hostnames, ok := hostsByProject[state.Spec.ProjectID]
	if !ok {
		// No noisy project, nothing to do.
		return nil
	}
	// Downvote the hosts this project is currently running on.
	for _, hostname := range hostnames {
		state.Vote(hostname, s.ActivationOnHit)
	}
	return nil
}

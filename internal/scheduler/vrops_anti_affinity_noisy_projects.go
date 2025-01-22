// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
)

type vROpsAntiAffinityNoisyProjectsStep struct {
	DB                db.DB
	AvgCPUThreshold   any
	runTimer          prometheus.Observer
	weightModObserver prometheus.Observer
}

func NewVROpsAntiAffinityNoisyProjectsStep(opts map[string]any, db db.DB, m monitor) PipelineStep {
	stepName := "vrops_anti_affinity_noisy_projects"
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(stepName)
	}
	var weightModObserver prometheus.Observer
	if m.stepWeightModObserver != nil {
		weightModObserver = m.stepWeightModObserver.WithLabelValues(stepName)
	}
	return &vROpsAntiAffinityNoisyProjectsStep{
		DB:                db,
		AvgCPUThreshold:   opts["avgCPUThreshold"],
		runTimer:          runTimer,
		weightModObserver: weightModObserver,
	}
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *vROpsAntiAffinityNoisyProjectsStep) Run(state *pipelineState) error {
	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	logging.Log.Debug("scheduler: anti-affinity - noisy projects")

	// If the average CPU usage is above the threshold, the project is considered noisy.
	var noisyProjects []features.VROpsProjectNoisiness
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
	val, ok := hostsByProject[state.Spec.ProjectID]
	if !ok {
		// No noisy project, nothing to do.
		return nil
	}
	// Downvote the hosts this project is currently running on.
	var modifiedWeights = 0
	for i := range state.Hosts {
		for _, host := range val {
			if state.Hosts[i].ComputeHost == host {
				state.Weights[state.Hosts[i].ComputeHost] = 0.0
				modifiedWeights++
				logging.Log.Debug(
					"scheduler: downvoting host",
					"host", host,
					"project", state.Spec.ProjectID,
				)
			}
		}
	}
	if s.weightModObserver != nil {
		s.weightModObserver.Observe(float64(modifiedWeights))
	}
	return nil
}

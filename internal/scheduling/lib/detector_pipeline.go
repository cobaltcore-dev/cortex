// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DetectorPipeline[DetectionType Detection] struct {
	// Kubernetes client to create descheduling resources.
	client.Client
	// Cycle detector to avoid cycles in descheduling.
	CycleDetector CycleDetector[DetectionType]
	// Monitor to use for tracking the pipeline.
	Monitor DetectorPipelineMonitor

	// The order in which scheduler steps are applied, by their step name.
	order []string
	// The steps by their name.
	steps map[string]Detector[DetectionType]
}

func (p *DetectorPipeline[DetectionType]) Init(
	ctx context.Context,
	confedSteps []v1alpha1.DetectorSpec,
	supportedSteps map[string]Detector[DetectionType],
) (nonCriticalErr, criticalErr error) {

	p.order = []string{}
	// Load all steps from the configuration.
	p.steps = make(map[string]Detector[DetectionType], len(confedSteps))
	for _, stepConf := range confedSteps {
		step, ok := supportedSteps[stepConf.Name]
		if !ok {
			nonCriticalErr = errors.New("descheduler: unsupported step name: " + stepConf.Name)
			continue // Descheduler steps are optional.
		}
		step = monitorDetector(step, stepConf, p.Monitor)
		if err := step.Init(ctx, p.Client, stepConf); err != nil {
			nonCriticalErr = errors.New("descheduler: failed to initialize step " + stepConf.Name + ": " + err.Error())
			continue // Descheduler steps are optional.
		}
		p.steps[stepConf.Name] = step
		p.order = append(p.order, stepConf.Name)
		slog.Info("descheduler: added step", "name", stepConf.Name)
	}
	return nonCriticalErr, nil // At the moment, there are no critical errors.
}

// Execute the descheduler steps in parallel and collect the decisions made by
// each step.
func (p *DetectorPipeline[DetectionType]) Run() map[string][]DetectionType {
	if p.Monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.Monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}
	var lock sync.Mutex
	decisionsByStep := map[string][]DetectionType{}
	var wg sync.WaitGroup
	for stepName, step := range p.steps {
		wg.Go(func() {
			slog.Info("descheduler: running step")
			decisions, err := step.Run()
			if errors.Is(err, ErrStepSkipped) {
				slog.Info("descheduler: step skipped")
				return
			}
			if err != nil {
				slog.Error("descheduler: failed to run step", "error", err)
				return
			}
			slog.Info("descheduler: finished step")
			lock.Lock()
			defer lock.Unlock()
			decisionsByStep[stepName] = decisions
		})
	}
	wg.Wait()
	return decisionsByStep
}

// Combine the decisions made by each step into a single list of resources to deschedule.
func (p *DetectorPipeline[DetectionType]) Combine(decisionsByStep map[string][]DetectionType) []DetectionType {
	// Order the step names to have a consistent order of processing.
	stepNames := make([]string, 0, len(decisionsByStep))
	for stepName := range decisionsByStep {
		stepNames = append(stepNames, stepName)
	}
	slices.Sort(stepNames)
	// If there are more than one decision for the same resource, we need to combine them.
	decisionsByResource := make(map[string][]DetectionType)
	for _, stepName := range stepNames {
		decisions := decisionsByStep[stepName]
		for _, decision := range decisions {
			decisionsByResource[decision.GetResource()] = append(
				decisionsByResource[decision.GetResource()], decision,
			)
		}
	}

	combinedDecisions := make([]DetectionType, 0, len(decisionsByResource))
	for resource, decisions := range decisionsByResource {
		if len(decisions) == 0 {
			continue
		}
		if len(decisions) == 1 {
			combinedDecisions = append(combinedDecisions, decisions[0])
			continue
		}
		// All hosts should be the same for the same resource.
		host := decisions[0].GetHost()
		sameHost := true
		for _, decision := range decisions[1:] {
			if decision.GetHost() != host {
				sameHost = false
				break
			}
		}
		if !sameHost {
			slog.Error("descheduler: conflicting hosts for combined decisions", "resource", resource, "decisions", decisions)
			continue
		}
		var reasonBuilder strings.Builder
		reasonBuilder.WriteString("multiple reasons: ")
		for i, decision := range decisions {
			if i > 0 {
				reasonBuilder.WriteString("; ")
			}
			reasonBuilder.WriteString(decision.GetReason())
		}
		mergedDecision := decisions[0]
		mergedDecision = mergedDecision.WithReason(reasonBuilder.String()).(DetectionType)
		combinedDecisions = append(combinedDecisions, mergedDecision)
	}

	slog.Info("descheduler: combined decisions", "combined", combinedDecisions)
	return combinedDecisions
}

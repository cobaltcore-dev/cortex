// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/prometheus/client_golang/prometheus"
)

// Wraps a scheduler step to monitor its execution.
type StepMonitor[RequestType PipelineRequest] struct {
	// Mixin that can be embedded in a step to provide some activation function tooling.
	ActivationFunction

	// The pipeline name to which this step belongs.
	pipelineName string

	// The wrapped scheduler step to monitor.
	Step Step[RequestType]
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A metric to monitor how much the step modifies the weights of the subjects.
	stepSubjectWeight *prometheus.GaugeVec
	// A metric to observe how many subjects are removed from the state.
	removedSubjectsObserver prometheus.Observer
	// A metric measuring where the subject at a given index came from originally.
	stepReorderingsObserver *prometheus.HistogramVec
	// A metric measuring the impact of the step on the subjects.
	stepImpactObserver *prometheus.HistogramVec
}

// Get the name of the wrapped step.
func (s *StepMonitor[RequestType]) GetName() string {
	return s.Step.GetName()
}

// Get the alias of the wrapped step.
func (s *StepMonitor[RequestType]) GetAlias() string {
	return s.Step.GetAlias()
}

// Initialize the wrapped step with the database and options.
func (s *StepMonitor[RequestType]) Init(alias string, db db.DB, opts conf.RawOpts) error {
	return s.Step.Init(alias, db, opts)
}

// Schedule using the wrapped step and measure the time it takes.
func MonitorStep[RequestType PipelineRequest](step Step[RequestType], m PipelineMonitor) *StepMonitor[RequestType] {
	stepName := step.GetName()
	stepAlias := step.GetAlias()
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(m.PipelineName, stepName, stepAlias)
	}
	var removedSubjectsObserver prometheus.Observer
	if m.stepRemovedSubjectsObserver != nil {
		removedSubjectsObserver = m.stepRemovedSubjectsObserver.WithLabelValues(m.PipelineName, stepName, stepAlias)
	}
	return &StepMonitor[RequestType]{
		Step:                    step,
		pipelineName:            m.PipelineName,
		runTimer:                runTimer,
		stepSubjectWeight:       m.stepSubjectWeight,
		removedSubjectsObserver: removedSubjectsObserver,
		stepReorderingsObserver: m.stepReorderingsObserver,
		stepImpactObserver:      m.stepImpactObserver,
	}
}

// Run the step and observe its execution.
func (s *StepMonitor[RequestType]) Run(traceLog *slog.Logger, request RequestType) (*StepResult, error) {
	stepName := s.GetName()
	stepAlias := s.GetAlias()

	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	inWeights := request.GetWeights()
	stepResult, err := s.Step.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	traceLog.Info(
		"scheduler: finished step", "name", stepName, "alias", stepAlias,
		"inWeights", inWeights, "outWeights", stepResult.Activations,
	)

	// Observe how much the step modifies the weights of the subjects.
	if s.stepSubjectWeight != nil {
		for subject, weight := range stepResult.Activations {
			s.stepSubjectWeight.WithLabelValues(s.pipelineName, subject, stepName, stepAlias).Add(weight)
			if weight != 0.0 {
				traceLog.Info(
					"scheduler: modified subject weight",
					"name", stepName, "alias", stepAlias, "weight", weight,
				)
			}
		}
	}

	// Observe how many subjects are removed from the state.
	subjectsIn := request.GetSubjects()
	subjectsOut := slices.Collect(maps.Keys(stepResult.Activations))
	nSubjectsRemoved := len(subjectsIn) - len(subjectsOut)
	if nSubjectsRemoved < 0 {
		traceLog.Info(
			"scheduler: removed subjects",
			"name", stepName, "alias", stepAlias, "count", nSubjectsRemoved,
		)
	}
	if s.removedSubjectsObserver != nil {
		s.removedSubjectsObserver.Observe(float64(nSubjectsRemoved))
	}

	// Calculate additional metrics to see which subjects were reordered and how far.
	sort.Slice(subjectsIn, func(i, j int) bool {
		iSubject, jSubject := subjectsIn[i], subjectsIn[j]
		return s.Norm(inWeights[iSubject]) > s.Norm(inWeights[jSubject])
	})
	sort.Slice(subjectsOut, func(i, j int) bool {
		// Add the weights together to get an estimate how far this step alone
		// would have moved the subject.
		iSubject, jSubject := subjectsOut[i], subjectsOut[j]
		iSum := s.Norm(inWeights[iSubject]) + s.Norm(stepResult.Activations[iSubject])
		jSum := s.Norm(inWeights[jSubject]) + s.Norm(stepResult.Activations[jSubject])
		return iSum > jSum
	})
	for idx := range min(len(subjectsOut), 5) { // Look at the first 5 subjects.
		// The subject at this index was moved from its original position.
		// Observe how far it was moved.
		originalIdx := slices.Index(subjectsIn, subjectsOut[idx])
		if s.stepReorderingsObserver != nil {
			o := s.stepReorderingsObserver.WithLabelValues(s.pipelineName, stepName, stepAlias, strconv.Itoa(idx))
			o.Observe(float64(originalIdx))
		}
		traceLog.Info(
			"scheduler: reordered subject",
			"name", stepName, "alias", stepAlias, "subject", subjectsOut[idx],
			"originalIdx", originalIdx, "newIdx", idx,
		)
	}

	// Based on the provided step statistics, log something like this:
	// max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	for statName, statData := range stepResult.Statistics {
		if statData.Subjects == nil {
			continue
		}
		msg := "scheduler: statistics for step " + stepName + " (" + stepAlias + ")"
		msg += " -- " + statName + ""
		before := ""
		for i, subject := range subjectsIn {
			if subjectStat, ok := statData.Subjects[subject]; ok {
				before += strconv.FormatFloat(subjectStat, 'f', 2, 64) + " " + statData.Unit
			} else {
				before += "-"
			}
			if i < len(subjectsIn)-1 {
				before += ", "
			}
		}
		after := ""
		for i, subject := range subjectsOut {
			if subjectStat, ok := statData.Subjects[subject]; ok {
				after += strconv.FormatFloat(subjectStat, 'f', 2, 64) + " " + statData.Unit
			} else {
				after += "-"
			}
			if i < len(subjectsOut)-1 {
				after += ", "
			}
		}
		traceLog.Info(msg, "before", before, "after", after)
	}

	// Calculate the impact for each recorded stat.
	for statName, statData := range stepResult.Statistics {
		if statData.Subjects == nil {
			continue
		}
		impact, err := impact(subjectsIn, subjectsOut, statData.Subjects, 5)
		if err != nil {
			traceLog.Error(
				"scheduler: error calculating impact",
				"name", stepName, "alias", stepAlias, "stat", statName, "error", err,
			)
			continue
		}
		if s.stepImpactObserver != nil {
			stepImpactObserver := s.stepImpactObserver.
				WithLabelValues(s.pipelineName, stepName, stepAlias, statName, statData.Unit)
			stepImpactObserver.Observe(impact)
		}
		traceLog.Info(
			"scheduler: impact for step",
			"name", stepName, "alias", stepAlias, "stat", statName,
			"unit", statData.Unit, "impact", impact,
		)
	}

	return stepResult, nil
}

// Calculate the impact of a scheduler step by comparing the before and after states.
// The impact is calculated as the sum of the absolute differences in the
// indices of the subjects in the before and after states, multiplied by the
// absolute difference in the statistics at those indices.
func impact(before, after []string, stats map[string]float64, topK int) (float64, error) {
	impact := 0.0
	for newIdx, subject := range after {
		if newIdx >= topK {
			break
		}
		// Since we are looking at small sets, this is likely faster
		// than creating the map and using it.
		oldIdx := slices.Index(before, subject)
		if oldIdx < 0 {
			// This case should not happen, because the scheduler step doesn't
			// add new subjects, it only reorders existing ones or filters.
			return 0, fmt.Errorf("subject %s not found in before state", subject)
		}
		if oldIdx == newIdx {
			// No impact if the subject stayed at the same index.
			continue
		}
		oldStatAtIdx := stats[before[newIdx]]
		newStatAtIdx := stats[subject]

		idxDisplacement := math.Abs(float64(oldIdx - newIdx))
		statDifference := math.Abs(oldStatAtIdx - newStatAtIdx)
		subimpact := idxDisplacement * statDifference
		impact += subimpact

		slog.Debug(
			"scheduler: impact calculation",
			"subject", subject,
			"oldIdx", oldIdx,
			"newIdx", newIdx,
			"idxDisplacement", idxDisplacement,
			"oldStatAtIdx", oldStatAtIdx,
			"newStatAtIdx", newStatAtIdx,
			"statDifference", statDifference,
			"subimpact", subimpact,
		)
	}
	slog.Debug(
		"scheduler: total impact",
		"impact", impact,
		"subjectsBefore", before,
		"subjectsAfter", after,
		"stats", stats,
	)

	return impact, nil
}

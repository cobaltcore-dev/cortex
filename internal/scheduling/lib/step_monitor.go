// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Wraps a scheduler step to monitor its execution.
type StepMonitor[RequestType PipelineRequest] struct {
	// Mixin that can be embedded in a step to provide some activation function tooling.
	ActivationFunction

	// The pipeline name to which this step belongs.
	pipelineName string
	// The name of this step.
	stepName string

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

// Initialize the wrapped step with the database and options.
func (s *StepMonitor[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	return s.Step.Init(ctx, client, step)
}

// Schedule using the wrapped step and measure the time it takes.
func monitorStep[RequestType PipelineRequest](
	_ context.Context,
	_ client.Client,
	step v1alpha1.Step,
	impl Step[RequestType],
	m PipelineMonitor,
) *StepMonitor[RequestType] {

	stepName := step.Namespace + "/" + step.Name
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.
			WithLabelValues(m.PipelineName, stepName)
	}
	var removedSubjectsObserver prometheus.Observer
	if m.stepRemovedSubjectsObserver != nil {
		removedSubjectsObserver = m.stepRemovedSubjectsObserver.
			WithLabelValues(m.PipelineName, stepName)
	}
	return &StepMonitor[RequestType]{
		Step:                    impl,
		stepName:                stepName,
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
		"scheduler: finished step", "name", s.stepName,
		"inWeights", inWeights, "outWeights", stepResult.Activations,
	)

	// Observe how much the step modifies the weights of the subjects.
	if s.stepSubjectWeight != nil {
		for subject, weight := range stepResult.Activations {
			s.stepSubjectWeight.
				WithLabelValues(s.pipelineName, subject, s.stepName).
				Add(weight)
			if weight != 0.0 {
				traceLog.Info(
					"scheduler: modified subject weight",
					"name", s.stepName, "weight", weight,
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
			"name", s.stepName, "count", nSubjectsRemoved,
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
			o := s.stepReorderingsObserver.
				WithLabelValues(s.pipelineName, s.stepName, strconv.Itoa(idx))
			o.Observe(float64(originalIdx))
		}
		traceLog.Info(
			"scheduler: reordered subject",
			"name", s.stepName, "subject", subjectsOut[idx],
			"originalIdx", originalIdx, "newIdx", idx,
		)
	}

	// Based on the provided step statistics, log something like this:
	// max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	for statName, statData := range stepResult.Statistics {
		if statData.Subjects == nil {
			continue
		}
		msg := "scheduler: statistics for step " + s.stepName
		msg += " -- " + statName + ""
		var beforeBuilder strings.Builder
		for i, subject := range subjectsIn {
			if subjectStat, ok := statData.Subjects[subject]; ok {
				beforeBuilder.WriteString(strconv.FormatFloat(subjectStat, 'f', 2, 64))
				beforeBuilder.WriteString(" ")
				beforeBuilder.WriteString(statData.Unit)
			} else {
				beforeBuilder.WriteString("-")
			}
			if i < len(subjectsIn)-1 {
				beforeBuilder.WriteString(", ")
			}
		}
		before := beforeBuilder.String()
		var afterBuilder strings.Builder
		for i, subject := range subjectsOut {
			if subjectStat, ok := statData.Subjects[subject]; ok {
				afterBuilder.WriteString(strconv.FormatFloat(subjectStat, 'f', 2, 64))
				afterBuilder.WriteString(" ")
				afterBuilder.WriteString(statData.Unit)
			} else {
				afterBuilder.WriteString("-")
			}
			if i < len(subjectsOut)-1 {
				afterBuilder.WriteString(", ")
			}
		}
		after := afterBuilder.String()
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
				"name", s.stepName, "stat", statName, "error", err,
			)
			continue
		}
		if s.stepImpactObserver != nil {
			stepImpactObserver := s.stepImpactObserver.
				WithLabelValues(s.pipelineName, s.stepName, statName, statData.Unit)
			stepImpactObserver.Observe(impact)
		}
		traceLog.Info(
			"scheduler: impact for step",
			"name", s.stepName, "stat", statName,
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

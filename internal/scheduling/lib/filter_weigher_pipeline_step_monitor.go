// Copyright SAP SE
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
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Wraps a scheduler step to monitor its execution.
type FilterWeigherPipelineStepMonitor[RequestType FilterWeigherPipelineRequest] struct {
	// Mixin that can be embedded in a step to provide some activation function tooling.
	ActivationFunction

	// The pipeline name to which this step belongs.
	pipelineName string
	// The name of this step.
	stepName string

	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A metric to monitor how much the step modifies the weights of the hosts.
	stepHostWeight *prometheus.GaugeVec
	// A metric to observe how many hosts are removed from the state.
	removedHostsObserver prometheus.Observer
	// A metric measuring where the host at a given index came from originally.
	stepReorderingsObserver *prometheus.HistogramVec
	// A metric measuring the impact of the step on the hosts.
	stepImpactObserver *prometheus.HistogramVec
}

// Schedule using the wrapped step and measure the time it takes.
func monitorStep[RequestType FilterWeigherPipelineRequest](stepName string, m FilterWeigherPipelineMonitor) *FilterWeigherPipelineStepMonitor[RequestType] {
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.
			WithLabelValues(m.PipelineName, stepName)
	}
	var removedHostsObserver prometheus.Observer
	if m.stepRemovedHostsObserver != nil {
		removedHostsObserver = m.stepRemovedHostsObserver.
			WithLabelValues(m.PipelineName, stepName)
	}
	return &FilterWeigherPipelineStepMonitor[RequestType]{
		runTimer:                runTimer,
		stepName:                stepName,
		pipelineName:            m.PipelineName,
		stepHostWeight:          m.stepHostWeight,
		removedHostsObserver:    removedHostsObserver,
		stepReorderingsObserver: m.stepReorderingsObserver,
		stepImpactObserver:      m.stepImpactObserver,
	}
}

// Run the step and observe its execution.
func (s *FilterWeigherPipelineStepMonitor[RequestType]) RunWrapped(
	traceLog *slog.Logger,
	request RequestType,
	step FilterWeigherPipelineStep[RequestType],
) (*FilterWeigherPipelineStepResult, error) {

	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	inWeights := request.GetWeights()
	stepResult, err := step.Run(traceLog, request)
	if err != nil {
		return nil, err
	}
	traceLog.Info(
		"scheduler: finished step", "name", s.stepName,
		"inWeights", inWeights, "outWeights", stepResult.Activations,
	)

	// Observe how much the step modifies the weights of the hosts.
	if s.stepHostWeight != nil {
		for host, weight := range stepResult.Activations {
			s.stepHostWeight.
				WithLabelValues(s.pipelineName, host, s.stepName).
				Add(weight)
			if weight != 0.0 {
				traceLog.Info(
					"scheduler: modified host weight",
					"name", s.stepName, "weight", weight,
				)
			}
		}
	}

	// Observe how many hosts are removed from the state.
	hostsIn := request.GetHosts()
	hostsOut := slices.Collect(maps.Keys(stepResult.Activations))
	nHostsRemoved := len(hostsIn) - len(hostsOut)
	if nHostsRemoved < 0 {
		traceLog.Info(
			"scheduler: removed hosts",
			"name", s.stepName, "count", nHostsRemoved,
		)
	}
	if s.removedHostsObserver != nil {
		s.removedHostsObserver.Observe(float64(nHostsRemoved))
	}

	// Calculate additional metrics to see which hosts were reordered and how far.
	sort.Slice(hostsIn, func(i, j int) bool {
		iHost, jHost := hostsIn[i], hostsIn[j]
		return s.Norm(inWeights[iHost]) > s.Norm(inWeights[jHost])
	})
	sort.Slice(hostsOut, func(i, j int) bool {
		// Add the weights together to get an estimate how far this step alone
		// would have moved the host.
		iHost, jHost := hostsOut[i], hostsOut[j]
		iSum := s.Norm(inWeights[iHost]) + s.Norm(stepResult.Activations[iHost])
		jSum := s.Norm(inWeights[jHost]) + s.Norm(stepResult.Activations[jHost])
		return iSum > jSum
	})
	for idx := range min(len(hostsOut), 5) { // Look at the first 5 hosts.
		// The host at this index was moved from its original position.
		// Observe how far it was moved.
		originalIdx := slices.Index(hostsIn, hostsOut[idx])
		if s.stepReorderingsObserver != nil {
			o := s.stepReorderingsObserver.
				WithLabelValues(s.pipelineName, s.stepName, strconv.Itoa(idx))
			o.Observe(float64(originalIdx))
		}
		traceLog.Info(
			"scheduler: reordered host",
			"name", s.stepName, "host", hostsOut[idx],
			"originalIdx", originalIdx, "newIdx", idx,
		)
	}

	// Based on the provided step statistics, log something like this:
	// max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	for statName, statData := range stepResult.Statistics {
		if statData.Hosts == nil {
			continue
		}
		msg := "scheduler: statistics for step " + s.stepName
		msg += " -- " + statName + ""
		var beforeBuilder strings.Builder
		for i, host := range hostsIn {
			if hostStat, ok := statData.Hosts[host]; ok {
				beforeBuilder.WriteString(strconv.FormatFloat(hostStat, 'f', 2, 64))
				beforeBuilder.WriteString(" ")
				beforeBuilder.WriteString(statData.Unit)
			} else {
				beforeBuilder.WriteString("-")
			}
			if i < len(hostsIn)-1 {
				beforeBuilder.WriteString(", ")
			}
		}
		before := beforeBuilder.String()
		var afterBuilder strings.Builder
		for i, host := range hostsOut {
			if hostStat, ok := statData.Hosts[host]; ok {
				afterBuilder.WriteString(strconv.FormatFloat(hostStat, 'f', 2, 64))
				afterBuilder.WriteString(" ")
				afterBuilder.WriteString(statData.Unit)
			} else {
				afterBuilder.WriteString("-")
			}
			if i < len(hostsOut)-1 {
				afterBuilder.WriteString(", ")
			}
		}
		after := afterBuilder.String()
		traceLog.Info(msg, "before", before, "after", after)
	}

	// Calculate the impact for each recorded stat.
	for statName, statData := range stepResult.Statistics {
		if statData.Hosts == nil {
			continue
		}
		impact, err := impact(hostsIn, hostsOut, statData.Hosts, 5)
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
// indices of the hosts in the before and after states, multiplied by the
// absolute difference in the statistics at those indices.
func impact(before, after []string, stats map[string]float64, topK int) (float64, error) {
	impact := 0.0
	for newIdx, host := range after {
		if newIdx >= topK {
			break
		}
		// Since we are looking at small sets, this is likely faster
		// than creating the map and using it.
		oldIdx := slices.Index(before, host)
		if oldIdx < 0 {
			// This case should not happen, because the scheduler step doesn't
			// add new hosts, it only reorders existing ones or filters.
			return 0, fmt.Errorf("host %s not found in before state", host)
		}
		if oldIdx == newIdx {
			// No impact if the host stayed at the same index.
			continue
		}
		oldStatAtIdx := stats[before[newIdx]]
		newStatAtIdx := stats[host]

		idxDisplacement := math.Abs(float64(oldIdx - newIdx))
		statDifference := math.Abs(oldStatAtIdx - newStatAtIdx)
		subimpact := idxDisplacement * statDifference
		impact += subimpact

		slog.Debug(
			"scheduler: impact calculation",
			"host", host,
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
		"hostsBefore", before,
		"hostsAfter", after,
		"stats", stats,
	)

	return impact, nil
}

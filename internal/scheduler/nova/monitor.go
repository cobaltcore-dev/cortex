// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

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
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

// Collection of Prometheus metrics to monitor scheduler pipeline
type Monitor struct {
	// A histogram to measure how long each step takes to run.
	stepRunTimer *prometheus.HistogramVec
	// A metric to monitor how much the step modifies the weights of the hosts.
	stepHostWeight *prometheus.GaugeVec
	// A histogram to observe how many hosts are removed from the state.
	stepRemovedHostsObserver *prometheus.HistogramVec
	// Histogram measuring where the host at a given index came from originally.
	stepReorderingsObserver *prometheus.HistogramVec
	// A histogram to observe the impact of the step on the hosts.
	stepImpactObserver *prometheus.HistogramVec
	// A histogram to measure how long the pipeline takes to run in total.
	pipelineRunTimer prometheus.Histogram
	// A histogram to observe the number of hosts going into the scheduler pipeline.
	hostNumberInObserver prometheus.Histogram
	// A histogram to observe the number of hosts coming out of the scheduler pipeline.
	hostNumberOutObserver prometheus.Histogram
	// Counter for the number of requests processed by the scheduler.
	requestCounter *prometheus.CounterVec
}

// Create a new scheduler monitor and register the necessary Prometheus metrics.
func NewSchedulerMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_step_run_duration_seconds",
		Help:    "Duration of scheduler pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	stepHostWeight := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_scheduler_nova_pipeline_step_weight_modification",
		Help: "Modification of host weight by scheduler pipeline step",
	}, []string{"host", "step"})
	stepRemovedHostsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_step_removed_hosts",
		Help:    "Number of hosts removed by scheduler pipeline step",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	}, []string{"step"})
	stepImpactObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_step_impact",
		Help:    "Impact of the step on the hosts",
		Buckets: prometheus.ExponentialBucketsRange(0.01, 1000, 20),
	}, []string{"step", "stat", "unit"})
	buckets := []float64{}
	buckets = append(buckets, prometheus.LinearBuckets(0, 1, 10)...)
	buckets = append(buckets, prometheus.LinearBuckets(10, 10, 4)...)
	buckets = append(buckets, prometheus.LinearBuckets(50, 50, 6)...)
	stepReorderingsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_step_shift_origin",
		Help:    "From which index of the host list the host came from originally.",
		Buckets: buckets,
	}, []string{"step", "outidx"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_run_duration_seconds",
		Help:    "Duration of scheduler pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	hostNumberInObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_host_number_in",
		Help:    "Number of hosts going into the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	hostNumberOutObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_nova_pipeline_host_number_out",
		Help:    "Number of hosts coming out of the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	requestCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_scheduler_nova_pipeline_requests_total",
		Help: "Total number of requests processed by the scheduler.",
	}, []string{"rebuild", "resize", "live", "vmware"})
	registry.MustRegister(
		stepRunTimer,
		stepHostWeight,
		stepRemovedHostsObserver,
		stepReorderingsObserver,
		stepImpactObserver,
		pipelineRunTimer,
		hostNumberInObserver,
		hostNumberOutObserver,
		requestCounter,
	)
	return Monitor{
		stepRunTimer:             stepRunTimer,
		stepHostWeight:           stepHostWeight,
		stepRemovedHostsObserver: stepRemovedHostsObserver,
		stepReorderingsObserver:  stepReorderingsObserver,
		stepImpactObserver:       stepImpactObserver,
		pipelineRunTimer:         pipelineRunTimer,
		hostNumberInObserver:     hostNumberInObserver,
		hostNumberOutObserver:    hostNumberOutObserver,
		requestCounter:           requestCounter,
	}
}

// Observe a scheduler pipeline result: hosts going in, and hosts going out.
func (m *Monitor) observePipelineResult(request api.Request, result []string) {
	// Observe the number of hosts going into the scheduler pipeline.
	if m.hostNumberInObserver != nil {
		m.hostNumberInObserver.Observe(float64(len(request.GetHosts())))
	}
	// Observe the number of hosts coming out of the scheduler pipeline.
	if m.hostNumberOutObserver != nil {
		m.hostNumberOutObserver.Observe(float64(len(result)))
	}
	// Observe the number of requests processed by the scheduler.
	if m.requestCounter != nil {
		m.requestCounter.WithLabelValues(
			strconv.FormatBool(request.GetRebuild()),
			strconv.FormatBool(request.GetResize()),
			strconv.FormatBool(request.GetLive()),
			strconv.FormatBool(request.GetVMware()),
		).Inc()
	}
}

// Wraps a scheduler step to monitor its execution.
type StepMonitor struct {
	// Mixin that can be embedded in a step to provide some activation function tooling.
	scheduler.ActivationFunction

	// The wrapped scheduler step to monitor.
	Step plugins.Step
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

// Get the name of the wrapped step.
func (s *StepMonitor) GetName() string {
	return s.Step.GetName()
}

// Initialize the wrapped step with the database and options.
func (s *StepMonitor) Init(db db.DB, opts conf.RawOpts) error {
	return s.Step.Init(db, opts)
}

// Schedule using the wrapped step and measure the time it takes.
func monitorStep(step plugins.Step, m Monitor) *StepMonitor {
	stepName := step.GetName()
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(stepName)
	}
	var removedHostsObserver prometheus.Observer
	if m.stepRemovedHostsObserver != nil {
		removedHostsObserver = m.stepRemovedHostsObserver.WithLabelValues(stepName)
	}
	return &StepMonitor{
		Step:                    step,
		runTimer:                runTimer,
		stepHostWeight:          m.stepHostWeight,
		removedHostsObserver:    removedHostsObserver,
		stepReorderingsObserver: m.stepReorderingsObserver,
		stepImpactObserver:      m.stepImpactObserver,
	}
}

// Run the step and observe its execution.
func (s *StepMonitor) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	stepName := s.GetName()

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
		"scheduler: finished step", "name", stepName,
		"inWeights", inWeights, "outWeights", stepResult.Activations,
	)

	// Observe how much the step modifies the weights of the hosts.
	if s.stepHostWeight != nil {
		for host, weight := range stepResult.Activations {
			s.stepHostWeight.WithLabelValues(host, stepName).Add(weight)
			if weight != 0.0 {
				traceLog.Info("scheduler: modified host weight", "name", stepName, "weight", weight)
			}
		}
	}

	// Observe how many hosts are removed from the state.
	hostsIn := request.GetHosts()
	hostsOut := slices.Collect(maps.Keys(stepResult.Activations))
	nHostsRemoved := len(hostsIn) - len(hostsOut)
	if nHostsRemoved < 0 {
		traceLog.Info("scheduler: removed hosts", "name", stepName, "count", nHostsRemoved)
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
			o := s.stepReorderingsObserver.WithLabelValues(stepName, strconv.Itoa(idx))
			o.Observe(float64(originalIdx))
		}
		traceLog.Info(
			"scheduler: reordered host",
			"name", stepName, "host", hostsOut[idx],
			"originalIdx", originalIdx, "newIdx", idx,
		)
	}

	// Based on the provided step statistics, log something like this:
	// max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	for statName, statData := range stepResult.Statistics {
		if statData.Hosts == nil {
			continue
		}
		msg := "scheduler: statistics for step " + stepName
		msg += " -- " + statName + ""
		before := ""
		for i, host := range hostsIn {
			if hostStat, ok := statData.Hosts[host]; ok {
				before += strconv.FormatFloat(hostStat, 'f', 2, 64) + " " + statData.Unit
			} else {
				before += "-"
			}
			if i < len(hostsIn)-1 {
				before += ", "
			}
		}
		after := ""
		for i, host := range hostsOut {
			if hostStat, ok := statData.Hosts[host]; ok {
				after += strconv.FormatFloat(hostStat, 'f', 2, 64) + " " + statData.Unit
			} else {
				after += "-"
			}
			if i < len(hostsOut)-1 {
				after += ", "
			}
		}
		traceLog.Info(msg, "before", before, "after", after)
	}

	// Calculate the impact for each recorded stat.
	for statName, statData := range stepResult.Statistics {
		if statData.Hosts == nil {
			continue
		}
		impact, err := impact(hostsIn, hostsOut, statData.Hosts, 5)
		if err != nil {
			traceLog.Error("scheduler: error calculating impact", "name", stepName, "stat", statName, "error", err)
			continue
		}
		if s.stepImpactObserver != nil {
			stepImpactObserver := s.stepImpactObserver.WithLabelValues(stepName, statName, statData.Unit)
			stepImpactObserver.Observe(impact)
		}
		traceLog.Info(
			"scheduler: impact for step",
			"name", stepName, "stat", statName, "unit", statData.Unit, "impact", impact,
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

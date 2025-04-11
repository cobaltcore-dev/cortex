// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
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
	// Observer for the number of reorderings conducted by the scheduler, by step.
	stepReorderingsObserver *prometheus.HistogramVec
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
		Name:    "cortex_scheduler_pipeline_step_run_duration_seconds",
		Help:    "Duration of scheduler pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	stepHostWeight := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_scheduler_pipeline_step_weight_modification",
		Help: "Modification of host weight by scheduler pipeline step",
	}, []string{"host", "step"})
	stepRemovedHostsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_removed_hosts",
		Help:    "Number of hosts removed by scheduler pipeline step",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	}, []string{"step"})
	stepReorderingsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_reorderings_levenshtein",
		Help:    "Number of reorderings conducted by the scheduler step. Defined as the Levenshtein distance between the hosts going in and out of the scheduler pipeline.",
		Buckets: prometheus.LinearBuckets(0, 1, 25),
	}, []string{"step"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_run_duration_seconds",
		Help:    "Duration of scheduler pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	hostNumberInObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_host_number_in",
		Help:    "Number of hosts going into the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	hostNumberOutObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_host_number_out",
		Help:    "Number of hosts coming out of the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	requestCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_scheduler_pipeline_requests_total",
		Help: "Total number of requests processed by the scheduler.",
	}, []string{"rebuild", "resize", "live", "vmware"})
	registry.MustRegister(
		stepRunTimer,
		stepHostWeight,
		stepRemovedHostsObserver,
		stepReorderingsObserver,
		pipelineRunTimer,
		hostNumberInObserver,
		hostNumberOutObserver,
		requestCounter,
	)
	return Monitor{
		stepRunTimer:             stepRunTimer,
		stepHostWeight:           stepHostWeight,
		stepRemovedHostsObserver: stepRemovedHostsObserver,
		pipelineRunTimer:         pipelineRunTimer,
		hostNumberInObserver:     hostNumberInObserver,
		hostNumberOutObserver:    hostNumberOutObserver,
		stepReorderingsObserver:  stepReorderingsObserver,
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
	// The wrapped scheduler step to monitor.
	Step plugins.Step
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A metric to monitor how much the step modifies the weights of the hosts.
	stepHostWeight *prometheus.GaugeVec
	// A metric to observe how many hosts are removed from the state.
	removedHostsObserver prometheus.Observer
	// A metric to observe the number of reorderings conducted by the scheduler.
	reorderingsObserver prometheus.Observer
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
	var reorderingsObserver prometheus.Observer
	if m.stepReorderingsObserver != nil {
		reorderingsObserver = m.stepReorderingsObserver.WithLabelValues(stepName)
	}
	return &StepMonitor{
		Step:                 step,
		runTimer:             runTimer,
		stepHostWeight:       m.stepHostWeight,
		removedHostsObserver: removedHostsObserver,
		reorderingsObserver:  reorderingsObserver,
	}
}

// Calculate the Levenshtein distance between two arrays.
//
// The Levenshtein distance is a measure of the difference between two sequences.
// It is defined as the minimum number of single-character edits (insertions,
// deletions, or substitutions) required to change one word into the other.
//
// We use the Levenshtein distance to see how many "edits" have been made to
// the hosts in the scheduler pipeline. This is useful to monitor how much
// the scheduler is changing the hosts in the pipeline, and to detect
// potential issues with the scheduler's behavior.
func levenshteinDistance(a, b []string) int {
	// Create a 2D array to store the distances.
	distance := make([][]int, len(a)+1)
	for i := range distance {
		distance[i] = make([]int, len(b)+1)
	}
	// Initialize the first row and column.
	for i := range distance {
		distance[i][0] = i
	}
	for j := range distance[0] {
		distance[0][j] = j
	}
	// Calculate the distances.
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			distance[i][j] = min(
				min(distance[i-1][j]+1, distance[i][j-1]+1),
				distance[i-1][j-1]+cost,
			)
		}
	}
	return distance[len(a)][len(b)]
}

// Run the step and observe its execution.
func (s *StepMonitor) Run(request api.Request) (map[string]float64, error) {
	stepName := s.GetName()

	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	weights, err := s.Step.Run(request)
	if err != nil {
		return nil, err
	}

	// Observe how much the step modifies the weights of the hosts.
	if s.stepHostWeight != nil {
		for host, weight := range weights {
			s.stepHostWeight.WithLabelValues(host, stepName).Add(weight)
			if weight != 0.0 {
				slog.Info("scheduler: modified host weight", "name", stepName, "weight", weight)
			}
		}
	}

	// Observe how many hosts are removed from the state.
	hostsInScenario := make(map[string]struct{})
	for _, host := range request.GetHosts() {
		hostsInScenario[host] = struct{}{}
	}
	nHostsRemoved := len(hostsInScenario) - len(weights)
	if nHostsRemoved < 0 {
		slog.Info("scheduler: removed hosts", "name", stepName, "count", nHostsRemoved)
	}
	if s.removedHostsObserver != nil {
		s.removedHostsObserver.Observe(float64(nHostsRemoved))
	}

	// Observe the number of reorderings conducted by the scheduler.
	if s.reorderingsObserver != nil {
		// Calculate the Levenshtein distance between the hosts going in and out.
		hosts := slices.Collect(maps.Keys(weights))
		sort.Slice(hosts, func(i, j int) bool {
			return weights[hosts[i]] > weights[hosts[j]]
		})
		distance := levenshteinDistance(request.GetHosts(), hosts)
		slog.Info("scheduler: reorderings", "name", stepName, "distance", distance, "hosts_in", request.GetHosts(), "hosts_out", hosts)
		s.reorderingsObserver.Observe(float64(distance))
	}

	return weights, nil
}

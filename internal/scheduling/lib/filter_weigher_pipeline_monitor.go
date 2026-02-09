// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Collection of Prometheus metrics to monitor scheduler pipeline
type FilterWeigherPipelineMonitor struct {
	// The pipeline name is used to differentiate between different pipelines.
	PipelineName string

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
	pipelineRunTimer *prometheus.HistogramVec
	// A histogram to observe the number of hosts going into the scheduler pipeline.
	hostNumberInObserver *prometheus.HistogramVec
	// A histogram to observe the number of hosts coming out of the scheduler pipeline.
	hostNumberOutObserver *prometheus.HistogramVec
	// Counter for the number of requests processed by the scheduler.
	requestCounter *prometheus.CounterVec
}

// Create a new scheduler monitor and register the necessary Prometheus metrics.
func NewPipelineMonitor() FilterWeigherPipelineMonitor {
	buckets := []float64{}
	buckets = append(buckets, prometheus.LinearBuckets(0, 1, 10)...)
	buckets = append(buckets, prometheus.LinearBuckets(10, 10, 4)...)
	buckets = append(buckets, prometheus.LinearBuckets(50, 50, 6)...)
	stepReorderingsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_filter_weigher_pipeline_step_shift_origin",
		Help:    "From which index of the host list the host came from originally.",
		Buckets: buckets,
	}, []string{"pipeline", "step", "outidx"})
	return FilterWeigherPipelineMonitor{
		stepRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_run_duration_seconds",
			Help:    "Duration of scheduler pipeline step run",
			Buckets: prometheus.DefBuckets,
		}, []string{"pipeline", "step"}),
		stepHostWeight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_filter_weigher_pipeline_step_weight_modification",
			Help: "Modification of host weight by scheduler pipeline step",
		}, []string{"pipeline", "host", "step"}),
		stepRemovedHostsObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_removed_hosts",
			Help:    "Number of hosts removed by scheduler pipeline step",
			Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
		}, []string{"pipeline", "step"}),
		stepReorderingsObserver: stepReorderingsObserver,
		stepImpactObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_impact",
			Help:    "Impact of the step on the hosts",
			Buckets: prometheus.ExponentialBucketsRange(0.01, 1000, 20),
		}, []string{"pipeline", "step", "stat", "unit"}),
		pipelineRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_run_duration_seconds",
			Help:    "Duration of scheduler pipeline run",
			Buckets: prometheus.DefBuckets,
		}, []string{"pipeline"}),
		hostNumberInObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_host_number_in",
			Help:    "Number of hosts going into the scheduler pipeline",
			Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
		}, []string{"pipeline"}),
		hostNumberOutObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_host_number_out",
			Help:    "Number of hosts coming out of the scheduler pipeline",
			Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
		}, []string{"pipeline"}),
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_filter_weigher_pipeline_requests_total",
			Help: "Total number of requests processed by the scheduler.",
		}, []string{"pipeline"}),
	}
}

// Get a copied pipeline monitor with the name set, after binding the metrics.
func (m FilterWeigherPipelineMonitor) SubPipeline(name string) FilterWeigherPipelineMonitor {
	cp := m
	cp.PipelineName = name
	return cp
}

// Observe a scheduler pipeline result: hosts going in, and hosts going out.
func (m *FilterWeigherPipelineMonitor) observePipelineResult(request FilterWeigherPipelineRequest, result []string) {
	// Observe the number of hosts going into the scheduler pipeline.
	if m.hostNumberInObserver != nil {
		m.hostNumberInObserver.
			WithLabelValues(m.PipelineName).
			Observe(float64(len(request.GetHosts())))
	}
	// Observe the number of hosts coming out of the scheduler pipeline.
	if m.hostNumberOutObserver != nil {
		m.hostNumberOutObserver.
			WithLabelValues(m.PipelineName).
			Observe(float64(len(result)))
	}
	// Observe the number of requests processed by the scheduler.
	if m.requestCounter != nil {
		m.requestCounter.
			WithLabelValues(m.PipelineName).
			Inc()
	}
}

func (m *FilterWeigherPipelineMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.stepRunTimer.Describe(ch)
	m.stepHostWeight.Describe(ch)
	m.stepRemovedHostsObserver.Describe(ch)
	m.stepReorderingsObserver.Describe(ch)
	m.stepImpactObserver.Describe(ch)
	m.pipelineRunTimer.Describe(ch)
	m.hostNumberInObserver.Describe(ch)
	m.hostNumberOutObserver.Describe(ch)
	m.requestCounter.Describe(ch)
}

func (m *FilterWeigherPipelineMonitor) Collect(ch chan<- prometheus.Metric) {
	m.stepRunTimer.Collect(ch)
	m.stepHostWeight.Collect(ch)
	m.stepRemovedHostsObserver.Collect(ch)
	m.stepReorderingsObserver.Collect(ch)
	m.stepImpactObserver.Collect(ch)
	m.pipelineRunTimer.Collect(ch)
	m.hostNumberInObserver.Collect(ch)
	m.hostNumberOutObserver.Collect(ch)
	m.requestCounter.Collect(ch)
}

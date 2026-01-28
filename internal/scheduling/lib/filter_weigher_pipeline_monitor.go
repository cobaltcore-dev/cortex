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
	// A metric to monitor how much the step modifies the weights of the subjects.
	stepSubjectWeight *prometheus.GaugeVec
	// A histogram to observe how many subjects are removed from the state.
	stepRemovedSubjectsObserver *prometheus.HistogramVec
	// Histogram measuring where the subject at a given index came from originally.
	stepReorderingsObserver *prometheus.HistogramVec
	// A histogram to observe the impact of the step on the subjects.
	stepImpactObserver *prometheus.HistogramVec
	// A histogram to measure how long the pipeline takes to run in total.
	pipelineRunTimer *prometheus.HistogramVec
	// A histogram to observe the number of subjects going into the scheduler pipeline.
	subjectNumberInObserver *prometheus.HistogramVec
	// A histogram to observe the number of subjects coming out of the scheduler pipeline.
	subjectNumberOutObserver *prometheus.HistogramVec
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
		Help:    "From which index of the subject list the subject came from originally.",
		Buckets: buckets,
	}, []string{"pipeline", "step", "outidx"})
	return FilterWeigherPipelineMonitor{
		stepRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_run_duration_seconds",
			Help:    "Duration of scheduler pipeline step run",
			Buckets: prometheus.DefBuckets,
		}, []string{"pipeline", "step"}),
		stepSubjectWeight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_filter_weigher_pipeline_step_weight_modification",
			Help: "Modification of subject weight by scheduler pipeline step",
		}, []string{"pipeline", "subject", "step"}),
		stepRemovedSubjectsObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_removed_subjects",
			Help:    "Number of subjects removed by scheduler pipeline step",
			Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
		}, []string{"pipeline", "step"}),
		stepReorderingsObserver: stepReorderingsObserver,
		stepImpactObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_step_impact",
			Help:    "Impact of the step on the subjects",
			Buckets: prometheus.ExponentialBucketsRange(0.01, 1000, 20),
		}, []string{"pipeline", "step", "stat", "unit"}),
		pipelineRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_run_duration_seconds",
			Help:    "Duration of scheduler pipeline run",
			Buckets: prometheus.DefBuckets,
		}, []string{"pipeline"}),
		subjectNumberInObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_subject_number_in",
			Help:    "Number of subjects going into the scheduler pipeline",
			Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
		}, []string{"pipeline"}),
		subjectNumberOutObserver: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_filter_weigher_pipeline_subject_number_out",
			Help:    "Number of subjects coming out of the scheduler pipeline",
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

// Observe a scheduler pipeline result: subjects going in, and subjects going out.
func (m *FilterWeigherPipelineMonitor) observePipelineResult(request FilterWeigherPipelineRequest, result []string) {
	// Observe the number of subjects going into the scheduler pipeline.
	if m.subjectNumberInObserver != nil {
		m.subjectNumberInObserver.
			WithLabelValues(m.PipelineName).
			Observe(float64(len(request.GetSubjects())))
	}
	// Observe the number of subjects coming out of the scheduler pipeline.
	if m.subjectNumberOutObserver != nil {
		m.subjectNumberOutObserver.
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
	m.stepSubjectWeight.Describe(ch)
	m.stepRemovedSubjectsObserver.Describe(ch)
	m.stepReorderingsObserver.Describe(ch)
	m.stepImpactObserver.Describe(ch)
	m.pipelineRunTimer.Describe(ch)
	m.subjectNumberInObserver.Describe(ch)
	m.subjectNumberOutObserver.Describe(ch)
	m.requestCounter.Describe(ch)
}

func (m *FilterWeigherPipelineMonitor) Collect(ch chan<- prometheus.Metric) {
	m.stepRunTimer.Collect(ch)
	m.stepSubjectWeight.Collect(ch)
	m.stepRemovedSubjectsObserver.Collect(ch)
	m.stepReorderingsObserver.Collect(ch)
	m.stepImpactObserver.Collect(ch)
	m.pipelineRunTimer.Collect(ch)
	m.subjectNumberInObserver.Collect(ch)
	m.subjectNumberOutObserver.Collect(ch)
	m.requestCounter.Collect(ch)
}

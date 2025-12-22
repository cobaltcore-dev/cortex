// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Monitor struct {
	// A histogram to measure how long each step takes to run.
	stepRunTimer *prometheus.HistogramVec
	// A counter to measure how many vm ids are selected for descheduling by each step.
	stepDeschedulingCounter *prometheus.GaugeVec
	// A histogram to measure how long the pipeline takes to run in total.
	pipelineRunTimer prometheus.Histogram
	// A histogram to measure how long it takes to deschedule a VM.
	deschedulingRunTimer *prometheus.HistogramVec

	// The name of the pipeline being monitored.
	PipelineName string
}

func NewPipelineMonitor() Monitor {
	return Monitor{
		stepRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_descheduler_pipeline_step_run_duration_seconds",
			Help:    "Duration of descheduler pipeline step run",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
		}, []string{"step"}),
		stepDeschedulingCounter: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_descheduler_pipeline_step_vms_descheduled",
			Help: "Number of vms descheduled by a descheduler pipeline step",
		}, []string{"step"}),
		pipelineRunTimer: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_descheduler_pipeline_run_duration_seconds",
			Help:    "Duration of descheduler pipeline run",
			Buckets: prometheus.DefBuckets,
		}),
		deschedulingRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_descheduler_pipeline_vm_descheduling_duration_seconds",
			Help:    "Duration of descheduling a VM in the descheduler pipeline",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
		}, []string{"error", "skipped", "source_host", "target_host", "vm_id"}),
	}
}

// Get a copied pipeline monitor with the name set, after binding the metrics.
func (m Monitor) SubPipeline(name string) Monitor {
	cp := m
	cp.PipelineName = name
	return cp
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.stepRunTimer.Describe(ch)
	m.stepDeschedulingCounter.Describe(ch)
	m.pipelineRunTimer.Describe(ch)
	m.deschedulingRunTimer.Describe(ch)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.stepRunTimer.Collect(ch)
	m.stepDeschedulingCounter.Collect(ch)
	m.pipelineRunTimer.Collect(ch)
	m.deschedulingRunTimer.Collect(ch)
}

type StepMonitor struct {
	// The step being monitored.
	step Step
	// The name of this step.
	stepName string
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A counter to measure how many vms are descheduled by this step.
	descheduledCounter prometheus.Counter
}

// Monitor a step by wrapping it with a StepMonitor.
func monitorStep(step Step, conf v1alpha1.Step, monitor Monitor) StepMonitor {
	name := conf.Namespace + "/" + conf.Name
	var runTimer prometheus.Observer
	if monitor.stepRunTimer != nil {
		runTimer = monitor.stepRunTimer.WithLabelValues(name)
	}
	var descheduledCounter prometheus.Counter
	if monitor.stepDeschedulingCounter != nil {
		descheduledCounter = monitor.stepDeschedulingCounter.WithLabelValues(name)
	}
	return StepMonitor{
		step:               step,
		stepName:           name,
		runTimer:           runTimer,
		descheduledCounter: descheduledCounter,
	}
}

// Initialize the step with the database and options.
func (m StepMonitor) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	return m.step.Init(ctx, client, step)
}

// Run the step and measure its execution time.
func (m StepMonitor) Run() ([]plugins.Decision, error) {
	if m.runTimer != nil {
		timer := prometheus.NewTimer(m.runTimer)
		defer timer.ObserveDuration()
	}
	vmsToDeschedule, err := m.step.Run()
	if err != nil {
		return nil, err
	}
	if m.descheduledCounter != nil {
		m.descheduledCounter.Add(float64(len(vmsToDeschedule)))
	}
	return vmsToDeschedule, nil
}

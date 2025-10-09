// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/prometheus/client_golang/prometheus"
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
}

func NewPipelineMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_descheduler_pipeline_step_run_duration_seconds",
		Help:    "Duration of descheduler pipeline step run",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
	}, []string{"step"})
	stepDeschedulingCounter := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_descheduler_pipeline_step_vms_descheduled",
		Help: "Number of vms descheduled by a descheduler pipeline step",
	}, []string{"step"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_descheduler_pipeline_run_duration_seconds",
		Help:    "Duration of descheduler pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	deschedulingRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_descheduler_pipeline_vm_descheduling_duration_seconds",
		Help:    "Duration of descheduling a VM in the descheduler pipeline",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
	}, []string{"error", "skipped", "source_host", "target_host", "vm_id"})
	registry.MustRegister(
		stepRunTimer,
		stepDeschedulingCounter,
		pipelineRunTimer,
		deschedulingRunTimer,
	)
	return Monitor{
		stepRunTimer:            stepRunTimer,
		stepDeschedulingCounter: stepDeschedulingCounter,
		pipelineRunTimer:        pipelineRunTimer,
		deschedulingRunTimer:    deschedulingRunTimer,
	}
}

type StepMonitor struct {
	// The step being monitored.
	step Step
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A counter to measure how many vms are descheduled by this step.
	descheduledCounter prometheus.Counter
}

// Monitor a step by wrapping it with a StepMonitor.
func monitorStep(step Step, monitor Monitor) StepMonitor {
	var runTimer prometheus.Observer
	if monitor.stepRunTimer != nil {
		runTimer = monitor.stepRunTimer.WithLabelValues(step.GetName())
	}
	var descheduledCounter prometheus.Counter
	if monitor.stepDeschedulingCounter != nil {
		descheduledCounter = monitor.stepDeschedulingCounter.WithLabelValues(step.GetName())
	}
	return StepMonitor{
		step:               step,
		runTimer:           runTimer,
		descheduledCounter: descheduledCounter,
	}
}

// Get the name of the step being monitored.
func (m StepMonitor) GetName() string {
	return m.step.GetName()
}

// Initialize the step with the database and options.
func (m StepMonitor) Init(db db.DB, opts conf.RawOpts) error {
	return m.step.Init(db, opts)
}

// Run the step and measure its execution time.
func (m StepMonitor) Run() ([]string, error) {
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

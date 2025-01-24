// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	stepRunTimer          *prometheus.HistogramVec
	stepWeightModObserver *prometheus.HistogramVec
	apiRequestsTimer      *prometheus.HistogramVec
	pipelineRunTimer      prometheus.Histogram
	hostNumberInObserver  prometheus.Histogram
	hostNumberOutObserver prometheus.Histogram
}

func NewSchedulerMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_run_duration_seconds",
		Help:    "Duration of scheduler pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	stepWeightModObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_weight_modification",
		Help:    "Modification of host weight by scheduler pipeline step",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	}, []string{"step"})
	apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_api_request_duration_seconds",
		Help:    "Duration of API requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status", "error"})
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
	registry.MustRegister(
		stepRunTimer,
		stepWeightModObserver,
		apiRequestsTimer,
		pipelineRunTimer,
		hostNumberInObserver,
		hostNumberOutObserver,
	)
	return Monitor{
		stepRunTimer:          stepRunTimer,
		stepWeightModObserver: stepWeightModObserver,
		apiRequestsTimer:      apiRequestsTimer,
		pipelineRunTimer:      pipelineRunTimer,
		hostNumberInObserver:  hostNumberInObserver,
		hostNumberOutObserver: hostNumberOutObserver,
	}
}

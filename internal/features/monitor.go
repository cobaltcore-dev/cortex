// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	stepRunTimer     *prometheus.HistogramVec
	pipelineRunTimer prometheus.Histogram
}

func NewPipelineMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_feature_pipeline_step_run_duration_seconds",
		Help:    "Duration of feature pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_feature_pipeline_run_duration_seconds",
		Help:    "Duration of feature pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	registry.MustRegister(
		stepRunTimer,
		pipelineRunTimer,
	)
	return Monitor{
		stepRunTimer:     stepRunTimer,
		pipelineRunTimer: pipelineRunTimer,
	}
}

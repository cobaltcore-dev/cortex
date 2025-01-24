// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	PipelineRunTimer                *prometheus.HistogramVec
	PipelineObjectsGauge            *prometheus.GaugeVec
	PipelineRequestTimer            *prometheus.HistogramVec
	PipelineRequestProcessedCounter *prometheus.CounterVec
}

func NewSyncMonitor(registry *monitoring.Registry) Monitor {
	pipelineRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_sync_run_duration_seconds",
		Help:    "Duration of sync run",
		Buckets: prometheus.DefBuckets,
	}, []string{"datasource"})
	pipelineObjectsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_sync_objects",
		Help: "Number of objects synced",
	}, []string{"datasource"})
	pipelineRequestTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_sync_request_duration_seconds",
		Help:    "Duration of sync request",
		Buckets: prometheus.DefBuckets,
	}, []string{"datasource"})
	pipelineRequestProcessedCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_sync_request_processed_total",
		Help: "Number of processed sync requests",
	}, []string{"datasource"})
	registry.MustRegister(
		pipelineRunTimer,
		pipelineObjectsGauge,
		pipelineRequestTimer,
		pipelineRequestProcessedCounter,
	)
	return Monitor{
		PipelineRunTimer:                pipelineRunTimer,
		PipelineObjectsGauge:            pipelineObjectsGauge,
		PipelineRequestTimer:            pipelineRequestTimer,
		PipelineRequestProcessedCounter: pipelineRequestProcessedCounter,
	}
}

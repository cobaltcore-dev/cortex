// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
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

type FeatureExtractorMonitor[F plugins.FeatureExtractor] struct {
	// The wrapped feature extractor to monitor.
	FeatureExtractor F
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
}

func (m FeatureExtractorMonitor[F]) GetName() string {
	// Return the name of the wrapped feature extractor.
	return m.FeatureExtractor.GetName()
}

func (m FeatureExtractorMonitor[F]) Init(db db.DB, opts map[string]any) error {
	// Configure the wrapped feature extractor.
	return m.FeatureExtractor.Init(db, opts)
}

func monitorFeatureExtractor[F plugins.FeatureExtractor](f F, m Monitor) FeatureExtractorMonitor[F] {
	featureExtractorName := f.GetName()
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(featureExtractorName)
	}
	return FeatureExtractorMonitor[F]{
		FeatureExtractor: f,
		runTimer:         runTimer,
	}
}

func (m FeatureExtractorMonitor[F]) Extract() error {
	slog.Info("features: extracting", "extractor", m.GetName())
	if m.runTimer != nil {
		timer := prometheus.NewTimer(m.runTimer)
		defer timer.ObserveDuration()
	}
	return m.FeatureExtractor.Extract()
}

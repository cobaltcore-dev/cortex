// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

// Collection of Prometheus metrics to monitor feature extraction.
type Monitor struct {
	// A histogram to measure how long each step takes to run.
	stepRunTimer *prometheus.HistogramVec
	// A counter to measure how many features are extracted by each step.
	stepFeatureCounter *prometheus.GaugeVec
	// A histogram to measure how long the pipeline takes to run in total.
	pipelineRunTimer prometheus.Histogram
}

// Create a new feature extraction monitor and register the
// necessary Prometheus metrics.
func NewPipelineMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_feature_pipeline_step_run_duration_seconds",
		Help:    "Duration of feature pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	stepFeatureCounter := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_feature_pipeline_step_features",
		Help: "Number of features extracted by a feature pipeline step",
	}, []string{"step"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_feature_pipeline_run_duration_seconds",
		Help:    "Duration of feature pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	registry.MustRegister(
		stepRunTimer,
		stepFeatureCounter,
		pipelineRunTimer,
	)
	return Monitor{
		stepRunTimer:       stepRunTimer,
		stepFeatureCounter: stepFeatureCounter,
		pipelineRunTimer:   pipelineRunTimer,
	}
}

// Wrapper for a feature extraction step that monitors the step's execution.
type FeatureExtractorMonitor[F plugins.FeatureExtractor] struct {
	// The wrapped feature extractor to monitor.
	FeatureExtractor F
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A counter to measure how many features are extracted by the step.
	featureCounter prometheus.Gauge
}

// Get the name of the wrapped feature extractor.
func (m FeatureExtractorMonitor[F]) GetName() string {
	// Return the name of the wrapped feature extractor.
	return m.FeatureExtractor.GetName()
}

// Get the message topics that trigger a re-execution of the wrapped feature extractor.
func (m FeatureExtractorMonitor[F]) Triggers() []string {
	// Return the triggers of the wrapped feature extractor.
	return m.FeatureExtractor.Triggers()
}

// Initialize the wrapped feature extractor with the database and options.
func (m FeatureExtractorMonitor[F]) Init(db db.DB, opts conf.RawOpts) error {
	// Configure the wrapped feature extractor.
	return m.FeatureExtractor.Init(db, opts)
}

// Extract features using the wrapped feature extractor and measure the time it takes.
func monitorFeatureExtractor[F plugins.FeatureExtractor](f F, m Monitor) FeatureExtractorMonitor[F] {
	featureExtractorName := f.GetName()
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(featureExtractorName)
	}
	var featureCounter prometheus.Gauge
	if m.stepFeatureCounter != nil {
		featureCounter = m.stepFeatureCounter.WithLabelValues(featureExtractorName)
	}
	return FeatureExtractorMonitor[F]{
		FeatureExtractor: f,
		runTimer:         runTimer,
		featureCounter:   featureCounter,
	}
}

// Run the wrapped feature extractor and measure the time it takes.
func (m FeatureExtractorMonitor[F]) Extract() ([]plugins.Feature, error) {
	slog.Info("features: extracting", "extractor", m.GetName())
	if m.runTimer != nil {
		timer := prometheus.NewTimer(m.runTimer)
		defer timer.ObserveDuration()
	}
	features, err := m.FeatureExtractor.Extract()
	if err != nil {
		return nil, err
	}
	if m.featureCounter != nil {
		m.featureCounter.Set(float64(len(features)))
	}
	return features, nil
}

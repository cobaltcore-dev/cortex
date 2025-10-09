// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/extractor/internal/conf"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
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
	// A counter to measure how many steps are skipped.
	stepSkipCounter *prometheus.CounterVec
}

// Create a new feature extraction monitor and register the
// necessary Prometheus metrics.
func NewPipelineMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_feature_pipeline_step_run_duration_seconds",
		Help:    "Duration of feature pipeline step run",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
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
	stepSkipCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_feature_pipeline_step_skipped",
		Help: "Number of times a feature pipeline step was skipped",
	}, []string{"step"})

	registry.MustRegister(
		stepRunTimer,
		stepFeatureCounter,
		pipelineRunTimer,
		stepSkipCounter,
	)
	return Monitor{
		stepRunTimer:       stepRunTimer,
		stepFeatureCounter: stepFeatureCounter,
		pipelineRunTimer:   pipelineRunTimer,
		stepSkipCounter:    stepSkipCounter,
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
	// A counter to measure how often an extractor is skipped.
	skipCounter prometheus.Counter
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
func (m FeatureExtractorMonitor[F]) Init(db db.DB, conf conf.FeatureExtractorConfig) error {
	// Configure the wrapped feature extractor.
	return m.FeatureExtractor.Init(db, conf)
}

func (m FeatureExtractorMonitor[F]) NeedsUpdate() bool {
	// Check if the wrapped feature extractor needs an update.
	return m.FeatureExtractor.NeedsUpdate()
}

func (m FeatureExtractorMonitor[F]) MarkAsUpdated() {
	// Mark the wrapped feature extractor as updated.
	m.FeatureExtractor.MarkAsUpdated()
}

func (m FeatureExtractorMonitor[F]) NextPossibleExecution() time.Time {
	// Return the next update timestamp of the wrapped feature extractor.
	return m.FeatureExtractor.NextPossibleExecution()
}

func (m FeatureExtractorMonitor[F]) NotifySkip() {
	// If the extractor is skipped, increment the skip counter if it exists.
	if m.skipCounter != nil {
		m.skipCounter.Inc()
	}
	m.FeatureExtractor.NotifySkip()
	slog.Info("features: skipping", "extractor", m.GetName())
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

	var skipCounter prometheus.Counter
	if m.stepSkipCounter != nil {
		skipCounter = m.stepSkipCounter.WithLabelValues(featureExtractorName)
	}

	return FeatureExtractorMonitor[F]{
		FeatureExtractor: f,
		runTimer:         runTimer,
		featureCounter:   featureCounter,
		skipCounter:      skipCounter,
	}
}

// Run the wrapped feature extractor and measure the time it takes.
func (m FeatureExtractorMonitor[F]) Extract() ([]plugins.Feature, error) {
	slog.Info("features: extracting", "extractor", m.GetName())

	// Only measure and record extraction duration if an update is actually needed.
	// This prevents unnecessary measurements from skewing the average and minimum values of the timing metric.
	if m.runTimer != nil && m.NeedsUpdate() {
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

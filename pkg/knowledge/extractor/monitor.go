// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("extractor")

// Collection of Prometheus metrics to monitor feature extraction.
type Monitor struct {
	// A histogram to measure how long each step takes to run.
	stepRunTimer *prometheus.HistogramVec
	// A counter to measure how many features are extracted by each step.
	stepFeatureCounter *prometheus.GaugeVec
	// A counter to measure how many steps are skipped.
	stepSkipCounter *prometheus.CounterVec
}

func NewMonitor() Monitor {
	return Monitor{
		stepRunTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_feature_pipeline_step_run_duration_seconds",
			Help:    "Duration of feature pipeline step run",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets
		}, []string{"step"}),
		stepFeatureCounter: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_feature_pipeline_step_features",
			Help: "Number of features extracted by a feature pipeline step",
		}, []string{"step"}),
		stepSkipCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_feature_pipeline_step_skipped",
			Help: "Number of times a feature pipeline step was skipped",
		}, []string{"step"}),
	}
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.stepRunTimer.Describe(ch)
	m.stepFeatureCounter.Describe(ch)
	m.stepSkipCounter.Describe(ch)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.stepRunTimer.Collect(ch)
	m.stepFeatureCounter.Collect(ch)
	m.stepSkipCounter.Collect(ch)
}

// Record a feature extraction run of the given step.
func (m *Monitor) observe(label string, duration time.Duration, nFeatures int) {
	if m.stepRunTimer != nil {
		m.stepRunTimer.WithLabelValues(label).Observe(duration.Seconds())
	}
	if m.stepFeatureCounter != nil {
		m.stepFeatureCounter.WithLabelValues(label).Set(float64(nFeatures))
	}
}

// Wrapper for a feature extractor that monitors its extraction runs.
type FeatureExtractorMonitor struct {
	// The wrapped feature extractor to monitor.
	FeatureExtractor
	// The label (name) of the extractor.
	label string
	// The monitor to record the extraction runs with.
	monitor Monitor
}

// Wrap the feature extractor so that its extraction runs are monitored.
func monitorFeatureExtractor(label string, f FeatureExtractor, m Monitor) FeatureExtractorMonitor {
	return FeatureExtractorMonitor{FeatureExtractor: f, label: label, monitor: m}
}

// Run the wrapped feature extractor and measure the time it takes.
func (m FeatureExtractorMonitor) Extract(ctx context.Context, inputs any) ([]Feature, error) {
	log.V(1).Info("features: extracting", "extractor", m.label)
	start := time.Now()
	features, err := m.FeatureExtractor.Extract(ctx, inputs)
	if err != nil {
		return nil, err
	}
	m.monitor.observe(m.label, time.Since(start), len(features))
	return features, nil
}

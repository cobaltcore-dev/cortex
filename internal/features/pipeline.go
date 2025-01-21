// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
)

// Configuration of feature extractors supported by the scheduler.
// The features to extract are defined in the configuration file.
var supportedExtractors = map[string]func(db.DB) FeatureExtractor{
	"vrops_hostsystem_resolver":             NewVROpsHostsystemResolver,
	"vrops_project_noisiness_extractor":     NewVROpsProjectNoisinessExtractor,
	"vrops_hostsystem_contention_extractor": NewVROpsHostsystemContentionExtractor,
}

type FeatureExtractor interface {
	Init() error
	Extract() error
}

type FeatureExtractorPipeline interface {
	Init()
	Extract()
}

type featureExtractorPipeline struct {
	FeatureExtractors []FeatureExtractor
	extractionCounter prometheus.Counter
	extractionTimer   prometheus.Histogram
}

// Create a new feature extractor pipeline with extractors contained in
// the configuration.
func NewPipeline(config conf.Config, database db.DB) FeatureExtractorPipeline {
	moduleConfig := config.GetFeaturesConfig()
	extractors := []FeatureExtractor{}
	for _, extractorConfig := range moduleConfig.Extractors {
		if extractorFunc, ok := supportedExtractors[extractorConfig.Name]; ok {
			extractor := extractorFunc(database)
			extractors = append(extractors, extractor)
		} else {
			panic("unknown feature extractor: " + extractorConfig.Name)
		}
	}
	extractionCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_features_pipeline_extract_runs",
		Help: "Total number of feature extractions",
	})
	extractionTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_features_pipeline_extract_duration_seconds",
		Help:    "Duration of feature extraction",
		Buckets: prometheus.DefBuckets,
	})
	prometheus.MustRegister(extractionCounter, extractionTimer)
	return &featureExtractorPipeline{
		FeatureExtractors: extractors,
		extractionCounter: extractionCounter,
		extractionTimer:   extractionTimer,
	}
}

// Creates the necessary database tables if they do not exist.
func (p *featureExtractorPipeline) Init() {
	for _, extractor := range p.FeatureExtractors {
		if err := extractor.Init(); err != nil {
			panic(err)
		}
	}
}

// Extract features from the data sources.
func (p *featureExtractorPipeline) Extract() {
	if p.extractionCounter != nil {
		p.extractionCounter.Inc()
	}
	if p.extractionTimer != nil {
		timer := prometheus.NewTimer(p.extractionTimer)
		defer timer.ObserveDuration()
	}

	for _, extractor := range p.FeatureExtractors {
		if err := extractor.Extract(); err != nil {
			logging.Log.Error("failed to extract features", "error", err)
		}
	}
}

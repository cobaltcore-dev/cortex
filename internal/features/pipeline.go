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
var supportedExtractors = map[string]func(db.DB, Monitor) FeatureExtractor{
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
	monitor           Monitor
}

// Create a new feature extractor pipeline with extractors contained in
// the configuration.
func NewPipeline(config conf.Config, database db.DB, m Monitor) FeatureExtractorPipeline {
	moduleConfig := config.GetFeaturesConfig()
	extractors := []FeatureExtractor{}
	for _, extractorConfig := range moduleConfig.Extractors {
		if extractorFunc, ok := supportedExtractors[extractorConfig.Name]; ok {
			extractor := extractorFunc(database, m)
			extractors = append(extractors, extractor)
		} else {
			panic("unknown feature extractor: " + extractorConfig.Name)
		}
	}
	return &featureExtractorPipeline{
		FeatureExtractors: extractors,
		monitor:           m,
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
	if p.monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}

	for _, extractor := range p.FeatureExtractors {
		if err := extractor.Extract(); err != nil {
			logging.Log.Error("failed to extract features", "error", err)
		}
	}
}

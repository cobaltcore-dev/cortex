// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/prometheus/client_golang/prometheus"
)

// Configuration of feature extractors supported by the scheduler.
// The features to extract are defined in the configuration file.
var supportedExtractors = []plugins.FeatureExtractor{
	&vmware.VROpsHostsystemResolver{},
	&vmware.VROpsProjectNoisinessExtractor{},
	&vmware.VROpsHostsystemContentionExtractor{},
}

type FeatureExtractorPipeline struct {
	extractors []plugins.FeatureExtractor
	monitor    Monitor
}

// Create a new feature extractor pipeline with extractors contained in
// the configuration.
func NewPipeline(config conf.Config, database db.DB, m Monitor) FeatureExtractorPipeline {
	supportedExtractorsByName := make(map[string]plugins.FeatureExtractor)
	for _, extractor := range supportedExtractors {
		supportedExtractorsByName[extractor.GetName()] = extractor
	}
	extractors := []plugins.FeatureExtractor{}
	for _, extractorConfig := range config.GetFeaturesConfig().Extractors {
		if extractorFunc, ok := supportedExtractorsByName[extractorConfig.Name]; ok {
			wrappedExtractor := monitorFeatureExtractor(extractorFunc, m)
			if err := wrappedExtractor.Init(database, extractorConfig.Options); err != nil {
				panic("failed to initialize feature extractor: " + err.Error())
			}
			extractors = append(extractors, wrappedExtractor)
			slog.Info(
				"feature extractor: added extractor",
				"name", extractorConfig.Name,
				"options", extractorConfig.Options,
			)
		} else {
			panic("unknown feature extractor: " + extractorConfig.Name)
		}
	}
	return FeatureExtractorPipeline{extractors: extractors, monitor: m}
}

// Extract features from the data sources.
func (p *FeatureExtractorPipeline) Extract() {
	if p.monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}
	for _, extractor := range p.extractors {
		if err := extractor.Extract(); err != nil {
			slog.Error("failed to extract features", "error", err)
		}
	}
}

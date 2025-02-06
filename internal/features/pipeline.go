// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"log/slog"
	"sync"

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
	executionOrder [][]plugins.FeatureExtractor
	monitor        Monitor
}

// Create a new feature extractor pipeline with extractors contained in
// the configuration.
func NewPipeline(config conf.FeaturesConfig, database db.DB, m Monitor) FeatureExtractorPipeline {
	supportedExtractorsByName := make(map[string]plugins.FeatureExtractor)
	for _, extractor := range supportedExtractors {
		supportedExtractorsByName[extractor.GetName()] = extractor
	}

	// Load all extractors from the configuration.
	extractorsByName := make(map[string]plugins.FeatureExtractor)
	for _, extractorConfig := range config.Extractors {
		extractorFunc, ok := supportedExtractorsByName[extractorConfig.Name]
		if !ok {
			panic("unknown feature extractor: " + extractorConfig.Name)
		}
		wrappedExtractor := monitorFeatureExtractor(extractorFunc, m)
		if err := wrappedExtractor.Init(database, extractorConfig.Options); err != nil {
			panic("failed to initialize feature extractor: " + err.Error())
		}
		extractorsByName[extractorConfig.Name] = wrappedExtractor
		slog.Info(
			"feature extractor: added extractor",
			"name", extractorConfig.Name,
			"options", extractorConfig.Options,
		)
	}

	// Build the dependency graph and resolve the execution order.
	extractors := []plugins.FeatureExtractor{}
	extractorDependencies := make(map[plugins.FeatureExtractor][]plugins.FeatureExtractor)
	for _, extractorConfig := range config.Extractors {
		extractor := extractorsByName[extractorConfig.Name]
		extractors = append(extractors, extractor)
		dependencies := []plugins.FeatureExtractor{}
		for _, name := range extractorConfig.DependencyConfig.Features.ExtractorNames {
			dependency, ok := extractorsByName[name]
			if !ok {
				panic("unknown feature extractor: " + name)
			}
			dependencies = append(dependencies, dependency)
		}
		extractorDependencies[extractor] = dependencies
	}
	dependencyGraph := conf.DependencyGraph[plugins.FeatureExtractor]{
		Dependencies: extractorDependencies,
		Nodes:        extractors,
	}
	executionOrder := dependencyGraph.Resolve()

	// Print out the execution order to the log.
	slog.Info("feature extractor: dependency graph resolved")
	for i, extractors := range executionOrder {
		for _, extractor := range extractors {
			slog.Info(
				"feature extractor: execution order",
				"group", i, "name", extractor.GetName(),
			)
		}
	}

	return FeatureExtractorPipeline{executionOrder: executionOrder, monitor: m}
}

// Extract features from the data sources.
func (p *FeatureExtractorPipeline) Extract() {
	if p.monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}

	// Execute the extractors in groups of the execution order.
	for _, extractors := range p.executionOrder {
		var wg sync.WaitGroup
		for _, extractor := range extractors {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := extractor.Extract(); err != nil {
					slog.Error("feature extractor: failed to extract features", "error", err)
					return
				}
			}()
		}
		wg.Wait()
	}
}

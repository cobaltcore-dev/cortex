// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"log/slog"
	"slices"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/prometheus/client_golang/prometheus"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Configuration of feature extractors supported by the scheduler.
// The actual features to extract are defined in the configuration file.
var supportedExtractors = []plugins.FeatureExtractor{
	// VMware-specific extractors
	&vmware.VROpsHostsystemResolver{},
	&vmware.VROpsProjectNoisinessExtractor{},
	&vmware.VROpsHostsystemContentionExtractor{},
	// KVM-specific extractors
	&kvm.NodeExporterHostCPUUsageExtractor{},
	&kvm.NodeExporterHostMemoryActiveExtractor{},
	// Shared extractors
	&shared.FlavorHostSpaceExtractor{},
}

// Pipeline that contains multiple feature extractors and executes them.
type FeatureExtractorPipeline struct {
	// The order in which feature extractors are executed periodically.
	// For example, [[f1], [f2, f3], [f4]] means that f1 is executed first
	// followed by f2 and f3 in parallel, and finally f4.
	executionOrder [][]plugins.FeatureExtractor
	// The order in which feature extractors are executed when triggered by
	// a message on a topic (key). This will only be used if MQTT is enabled.
	// Dimensions: distinct subgraph depending on the topic -> step -> extractor.
	triggerExecutionOrder map[string][][][]plugins.FeatureExtractor
	// Monitor to use for tracking the pipeline.
	monitor Monitor
}

// Create a new feature extractor pipeline with extractors contained in
// the configuration. This function automatically resolves an execution
// graph to automate parallel execution of the individual feature extractors.
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

	// Resolve which feature extractors to execute when triggers are received.
	// First collect all triggers we are listening for.
	triggers := make(map[string]struct{})
	for _, extractor := range extractors {
		for _, topic := range extractor.Triggers() {
			triggers[topic] = struct{}{}
		}
	}
	// Then, for each topic, get the highest node in the dependency graph
	// that has a trigger for this topic and resolve its subgraph.
	triggerExecutionOrder := make(map[string][][][]plugins.FeatureExtractor)
	for topic := range triggers {
		condition := func(extractor plugins.FeatureExtractor) bool {
			return slices.Contains(extractor.Triggers(), topic)
		}
		subgraphs := dependencyGraph.DistinctSubgraphs(condition)
		triggerExecutionOrder[topic] = make([][][]plugins.FeatureExtractor, len(subgraphs))
		for i, subgraph := range subgraphs {
			triggerExecutionOrder[topic][i] = subgraph.Resolve()
		}
	}

	slog.Info(
		"feature extractor: resolved execution order",
		"all", executionOrder, "trigger", triggerExecutionOrder,
	)

	return FeatureExtractorPipeline{
		triggerExecutionOrder: triggerExecutionOrder,
		executionOrder:        executionOrder,
		monitor:               m,
	}
}

// Extract features from the data sources when triggered by MQTT messages.
// If mqtt is disabled, this function does nothing.
func (p *FeatureExtractorPipeline) ExtractOnTrigger() {
	// Subscribe to the MQTT topics that trigger the feature extraction.
	mqttClient := mqtt.NewClient() // Only initialized when mqtt is enabled.
	for topic, subgraphs := range p.triggerExecutionOrder {
		if err := mqttClient.Subscribe(topic, func(_ pahomqtt.Client, _ pahomqtt.Message) {
			for _, order := range subgraphs {
				slog.Info("triggered feature extractors by mqtt message", "topic", topic)
				p.extract(order)
			}
		}); err != nil {
			panic("failed to subscribe to topic: " + topic)
		}
	}
}

// Extract features from the data sources, in the sequence given by
// the automatically calculated execution order.
func (p *FeatureExtractorPipeline) Extract() {
	if p.monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}
	p.extract(p.executionOrder)
}

// Extract features in the sequence given by the execution order.
func (p *FeatureExtractorPipeline) extract(order [][]plugins.FeatureExtractor) {
	// Execute the extractors in groups of the execution order.
	for _, extractors := range order {
		var wg sync.WaitGroup
		for _, extractor := range extractors {
			wg.Add(1)
			go func(extractor plugins.FeatureExtractor) {
				defer wg.Done()
				if _, err := extractor.Extract(); err != nil {
					slog.Error("feature extractor: failed to extract features", "error", err)
					return
				}
			}(extractor)
		}
		wg.Wait()
	}
}

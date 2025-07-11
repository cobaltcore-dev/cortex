// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"log/slog"
	"slices"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Configuration of feature extractors supported by the scheduler.
var SupportedExtractors = []plugins.FeatureExtractor{
	// VMware-specific extractors
	&vmware.VROpsHostsystemResolver{},
	&vmware.VROpsProjectNoisinessExtractor{},
	&vmware.VROpsHostsystemContentionLongTermExtractor{},
	&vmware.VROpsHostsystemContentionShortTermExtractor{},
	// KVM-specific extractors
	&kvm.NodeExporterHostCPUUsageExtractor{},
	&kvm.NodeExporterHostMemoryActiveExtractor{},
	// NetApp-specific extractors
	&netapp.StoragePoolCPUUsageExtractor{},
	// Shared extractors
	&shared.HostUtilizationExtractor{},
	&shared.HostCapabilitiesExtractor{},
	&shared.VMHostResidencyExtractor{},
	&shared.VMLifeSpanHistogramExtractor{},
	&shared.HostDomainProjectExtractor{},
}

// Pipeline that contains multiple feature extractors and executes them.
type FeatureExtractorPipeline struct {
	// The dependency graph of the feature extractors, which is used to
	// determine the execution order of the feature extractors.
	//
	// For example, [[f1], [f2, f3], [f4]] means that f1 is executed first
	// followed by f2 and f3 in parallel, and finally f4.
	dependencyGraph conf.DependencyGraph[plugins.FeatureExtractor]
	// The resolved order in which feature extractors are executed when triggered
	// by a message on a topic (key).
	//
	// Dimensions: distinct subgraph depending on the topic -> step -> extractor.
	triggerExecutionOrder map[string][][][]plugins.FeatureExtractor
	// Database to store the extracted features.
	db db.DB
	// Config to use for the feature extractors.
	config conf.ExtractorConfig
	// Monitor to use for tracking the pipeline.
	monitor Monitor
	// MQTT client to publish the extracted features.
	mqttClient mqtt.Client
}

// Create a new feature extractor pipeline with extractors contained in the configuration.
func NewPipeline(config conf.ExtractorConfig, database db.DB, m Monitor, mqttClient mqtt.Client) FeatureExtractorPipeline {
	return FeatureExtractorPipeline{
		db:         database,
		config:     config,
		monitor:    m,
		mqttClient: mqttClient,
	}
}

// Initialize the feature extractors in the pipeline.
func (p *FeatureExtractorPipeline) Init(supportedExtractors []plugins.FeatureExtractor) {
	p.initDependencyGraph(supportedExtractors)
	p.initTriggerExecutionOrder()
}

// Initialize the execution order of the feature extractors.
func (p *FeatureExtractorPipeline) initDependencyGraph(supportedExtractors []plugins.FeatureExtractor) {
	supportedExtractorsByName := make(map[string]plugins.FeatureExtractor)
	for _, extractor := range supportedExtractors {
		supportedExtractorsByName[extractor.GetName()] = extractor
	}

	// Load all extractors from the configuration.
	extractorsByName := make(map[string]plugins.FeatureExtractor)
	for _, extractorConfig := range p.config.Plugins {
		extractorFunc, ok := supportedExtractorsByName[extractorConfig.Name]
		if !ok {
			panic("unknown feature extractor: " + extractorConfig.Name)
		}
		wrappedExtractor := monitorFeatureExtractor(extractorFunc, p.monitor)
		if err := wrappedExtractor.Init(p.db, extractorConfig); err != nil {
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
	for _, extractorConfig := range p.config.Plugins {
		extractor := extractorsByName[extractorConfig.Name]
		extractors = append(extractors, extractor)
		dependencies := []plugins.FeatureExtractor{}
		for _, name := range extractorConfig.Extractors {
			dependency, ok := extractorsByName[name]
			if !ok {
				panic("unknown feature extractor: " + name)
			}
			dependencies = append(dependencies, dependency)
		}
		extractorDependencies[extractor] = dependencies
	}
	p.dependencyGraph = conf.DependencyGraph[plugins.FeatureExtractor]{
		Dependencies: extractorDependencies,
		Nodes:        extractors,
	}
}

// Initialize the trigger execution order of the feature extractors.
func (p *FeatureExtractorPipeline) initTriggerExecutionOrder() {
	// Resolve which feature extractors to execute when triggers are received.
	// First collect all triggers we are listening for.
	triggers := make(map[string]struct{})
	for _, extractor := range p.dependencyGraph.Nodes {
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
		subgraphs := p.dependencyGraph.DistinctSubgraphs(condition)
		triggerExecutionOrder[topic] = make([][][]plugins.FeatureExtractor, len(subgraphs))
		for i, subgraph := range subgraphs {
			order, err := subgraph.Resolve()
			if err != nil {
				panic("failed to resolve dependency graph: " + err.Error())
			}
			triggerExecutionOrder[topic][i] = order
		}
	}
	p.triggerExecutionOrder = triggerExecutionOrder
	slog.Info(
		"feature extractor: resolved execution order",
		"order", triggerExecutionOrder, "trigger", triggerExecutionOrder,
	)
}

// Extract features from the data sources when triggered by MQTT messages.
// If mqtt is disabled, this function does nothing.
func (p *FeatureExtractorPipeline) ExtractOnTrigger() {
	// Subscribe to the MQTT topics that trigger the feature extraction.
	var lock sync.Mutex
	for topic, subgraphs := range p.triggerExecutionOrder {
		slog.Info("subscribing to mqtt topic", "topic", topic, "subgraphs", subgraphs)
		callback := func() {
			// Always calculate on one execution order only.
			// This will avoid the same feature extractor from being calculated
			// multiple times simultaneously, leading to race conditions.
			lock.Lock()
			defer lock.Unlock()

			for _, order := range subgraphs {
				slog.Info("triggered feature extractors by mqtt message", "topic", topic)
				p.extract(order)
			}
		}
		if err := p.mqttClient.Subscribe(topic, func(_ pahomqtt.Client, _ pahomqtt.Message) {
			// It's important to execute the callback in a goroutine.
			// Otherwise, the MQTT client will block until the callback
			// is finished, potentially leading to disconnects.
			go callback()
		}); err != nil {
			panic("failed to subscribe to topic: " + topic)
		}
	}
}

// Extract features in the sequence given by the execution order.
func (p *FeatureExtractorPipeline) extract(order [][]plugins.FeatureExtractor) {
	// Execute the extractors in groups of the execution order.
	for _, extractors := range order {
		var wg sync.WaitGroup
		for _, extractor := range extractors {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !extractor.NeedsUpdate() {
					slog.Info("feature extractor: skipping extraction", "extractor", extractor.GetName(), "nextExecution", extractor.NextPossibleExecution())
					extractor.NotifySkip()
					return
				}
				features, err := extractor.Extract()
				if err != nil {
					slog.Error("feature extractor: failed to extract features", "error", err)
					return
				}
				// Feature extractors should always calculate features, unless
				// their underlying data source is not complete yet. In this case,
				// we want to try again later, to avoid missing features.
				if len(features) > 0 {
					extractor.MarkAsUpdated()
				} else {
					slog.Warn("feature extractor extracted no features", "extractor", extractor.GetName())
				}
			}()
		}
		wg.Wait()
	}
}

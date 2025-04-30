// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
)

type mockFeatureExtractor struct {
	name       string
	triggers   []string
	features   []plugins.Feature
	initErr    error
	extractErr error
}

func (m *mockFeatureExtractor) Init(db db.DB, opts conf.RawOpts) error {
	return m.initErr
}

func (m *mockFeatureExtractor) Extract() ([]plugins.Feature, error) {
	return m.features, m.extractErr
}

func (m *mockFeatureExtractor) GetName() string {
	return m.name
}

func (m *mockFeatureExtractor) Triggers() []string {
	return m.triggers
}

func TestFeatureExtractorPipeline_Extract(t *testing.T) {
	// Test case: All extractors extract successfully
	pipeline := &FeatureExtractorPipeline{}
	pipeline.extract([][]plugins.FeatureExtractor{
		{&mockFeatureExtractor{}},
		{&mockFeatureExtractor{}},
	})

	// No errors means the test passed
}

func TestFeatureExtractorPipeline_Extract_Failure(t *testing.T) {
	// Test case: One extractor fails to extract
	pipeline := &FeatureExtractorPipeline{}
	pipeline.extract([][]plugins.FeatureExtractor{
		{&mockFeatureExtractor{}},
		{&mockFeatureExtractor{extractErr: errors.New("extract error")}},
	})

	// No panic means the test passed
}

func TestFeatureExtractorPipeline_InitDependencyGraph(t *testing.T) {
	// Mock configuration with two extractors and a dependency
	config := conf.FeaturesConfig{
		Plugins: []conf.FeatureExtractorConfig{
			{
				Name:    "extractor1",
				Options: conf.RawOpts{},
				DependencyConfig: conf.DependencyConfig{
					Features: conf.FeaturesDependency{
						// Extractor 1 depends on Extractor 2
						ExtractorNames: []string{"extractor2"},
					},
				},
			},
			{
				Name:    "extractor2",
				Options: conf.RawOpts{},
			},
			{
				Name:    "extractor3",
				Options: conf.RawOpts{},
				DependencyConfig: conf.DependencyConfig{
					Features: conf.FeaturesDependency{
						// Extractor 1 depends on Extractor 2
						ExtractorNames: []string{"extractor2"},
					},
				},
			},
		},
	}

	// Mock supported extractors
	supportedExtractors := []plugins.FeatureExtractor{
		&mockFeatureExtractor{name: "extractor1"},
		&mockFeatureExtractor{name: "extractor2"},
		&mockFeatureExtractor{name: "extractor3"},
	}

	pipeline := FeatureExtractorPipeline{
		config: config,
	}

	// Call the function
	pipeline.initDependencyGraph(supportedExtractors)

	// Assertions
	if len(pipeline.dependencyGraph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in the dependency graph, got %d", len(pipeline.dependencyGraph.Nodes))
	}

	if len(pipeline.dependencyGraph.Dependencies) != 3 {
		t.Fatalf("expected 3 dependencies in the dependency graph, got %d", len(pipeline.dependencyGraph.Dependencies))
	}

	// Need to compare the values like this since the map keys are pointers.
	getDeps := func(name string) []plugins.FeatureExtractor {
		for _, node := range pipeline.dependencyGraph.Nodes {
			if node.GetName() == name {
				return pipeline.dependencyGraph.Dependencies[node]
			}
		}
		return nil
	}

	if len(getDeps("extractor1")) != 1 {
		t.Fatalf("expected 1 dependency for extractor1, got %d", len(getDeps("extractor1")))
	}
	if len(getDeps("extractor2")) != 0 {
		t.Fatalf("expected 0 dependencies for extractor2, got %d", len(getDeps("extractor2")))
	}
	if len(getDeps("extractor3")) != 1 {
		t.Fatalf("expected 1 dependency for extractor3, got %d", len(getDeps("extractor3")))
	}
}

func TestFeatureExtractorPipeline_InitTriggerExecutionOrder(t *testing.T) {
	// Mock configuration with two extractors and triggers
	config := conf.FeaturesConfig{
		Plugins: []conf.FeatureExtractorConfig{
			{
				Name:    "extractor1",
				Options: conf.RawOpts{},
			},
			{
				Name:    "extractor2",
				Options: conf.RawOpts{},
			},
		},
	}

	// Mock supported extractors
	supportedExtractors := []plugins.FeatureExtractor{
		&mockFeatureExtractor{name: "extractor1", triggers: []string{"topic1"}},
		&mockFeatureExtractor{name: "extractor2", triggers: []string{"topic2"}},
	}

	pipeline := FeatureExtractorPipeline{
		config: config,
	}
	pipeline.initDependencyGraph(supportedExtractors)
	pipeline.initTriggerExecutionOrder()

	// Assertions
	if len(pipeline.triggerExecutionOrder) != 2 {
		t.Fatalf("expected 2 triggers in the trigger execution order, got %d", len(pipeline.triggerExecutionOrder))
	}

	if _, ok := pipeline.triggerExecutionOrder["topic1"]; !ok {
		t.Fatalf("expected triggerExecutionOrder to contain topic1")
	}

	if _, ok := pipeline.triggerExecutionOrder["topic2"]; !ok {
		t.Fatalf("expected triggerExecutionOrder to contain topic2")
	}
}

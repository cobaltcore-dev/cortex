// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

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
}

func NewFeatureExtractorPipeline(db db.DB) FeatureExtractorPipeline {
	return &featureExtractorPipeline{
		FeatureExtractors: []FeatureExtractor{
			// Resolve "hostsystem" label to Nova compute hosts.
			NewVROpsHostsystemResolver(db),
			// Extract how much resources projects consume on average.
			NewProjectNoisinessExtractor(db),
			// Extract how much CPU contention is seen on each compute host.
			NewHostsystemContentionExtractor(db),
		},
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
	for _, extractor := range p.FeatureExtractors {
		if err := extractor.Extract(); err != nil {
			logging.Log.Error("failed to extract features", "error", err)
		}
	}
}

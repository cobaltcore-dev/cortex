// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// Functions that create database schemas for features.
var schemaCreators = []func() error{
	projectNoisinessSchema,
}

// Functions that extract features from the data sources.
var featureExtractors = []func() error{
	projectNoisinessExtractor,
}

// Creates the necessary database tables if they do not exist.
func Init() {
	for _, schemaCreator := range schemaCreators {
		if err := schemaCreator(); err != nil {
			panic(err)
		}
	}
}

// Extract features from the data sources.
func Extract() {
	for _, featureExtractor := range featureExtractors {
		if err := featureExtractor(); err != nil {
			logging.Log.Error("failed to extract features", "error", err)
		}
	}
}

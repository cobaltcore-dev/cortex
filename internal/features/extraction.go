// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

var schemaCreators = []func() error{
	projectNoisinessSchema,
}

var featureExtractors = []func() error{
	projectNoisinessExtractor,
}

func Init() {
	for _, schemaCreator := range schemaCreators {
		if err := schemaCreator(); err != nil {
			panic(err)
		}
	}
}

func Extract() {
	for _, featureExtractor := range featureExtractors {
		if err := featureExtractor(); err != nil {
			logging.Log.Error("failed to extract features", "error", err)
		}
	}
}

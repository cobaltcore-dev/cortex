// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"gopkg.in/yaml.v2"
)

type FeatureExtractor interface {
	// Configure the feature extractor with a database and options.
	// This function should also create the needed database structures.
	Init(db db.DB, opts yaml.MapSlice) error
	// Extract features from the given data.
	Extract() error
	// Get the name of this feature extractor.
	// This name is used to identify the extractor in metrics, config, logs, etc.
	// Should be something like: "my_cool_feature_extractor".
	GetName() string
}

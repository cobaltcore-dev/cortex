// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/db"
)

// Each feature extractor must conform to this interface.
type FeatureExtractor interface {
	// Configure the feature extractor with a spec and (optional) databases.
	Init(datasourceDB *db.DB, extractorDB *db.DB, spec v1alpha1.KnowledgeSpec) error
	// Extract features from the given data.
	Extract() ([]Feature, error)
}

type Feature any

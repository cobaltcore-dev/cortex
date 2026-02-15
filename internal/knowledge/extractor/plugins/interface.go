// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Each feature extractor must conform to this interface.
type FeatureExtractor interface {
	// Configure the feature extractor with a spec and (optional) databases.
	Init(datasourceDB *db.DB, client client.Client, spec v1alpha1.KnowledgeSpec) error
	// Extract features from the given data.
	Extract(d []*v1alpha1.Datasource, k []*v1alpha1.Knowledge) ([]Feature, error)
}

type Feature any

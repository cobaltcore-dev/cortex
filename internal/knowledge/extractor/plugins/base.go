// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all extractors that provides some functionality
// that would otherwise be duplicated across all extractors.
type BaseExtractor[Opts any, Feature any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// Database connection where the datasources are stored.
	DB *db.DB
	// Kubernetes client to access other resources.
	Client client.Client
}

// Init the extractor with the database and options.
func (e *BaseExtractor[Opts, Feature]) Init(
	datasourceDB *db.DB, client client.Client, spec v1alpha1.KnowledgeSpec,
) error {

	rawOpts := conf.NewRawOpts(`{}`)
	if len(spec.Extractor.Config.Raw) > 0 {
		rawOpts = conf.NewRawOptsBytes(spec.Extractor.Config.Raw)
	}
	if err := e.Load(rawOpts); err != nil {
		return err
	}
	e.DB = datasourceDB
	e.Client = client
	return nil
}

// Extract the features directly from an sql query.
func (e *BaseExtractor[Opts, F]) ExtractSQL(query string) ([]Feature, error) {
	// This can happen when no datasource is provided that connects to a database.
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	var features []F
	if _, err := e.DB.Select(&features, query); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}

// Return the extracted features as a slice of generic features for counting.
func (e *BaseExtractor[Opts, F]) Extracted(fs []F) ([]Feature, error) {
	output := make([]Feature, len(fs))
	for i, f := range fs {
		output[i] = f
	}
	slog.Info("features: extracted", "count", len(output))
	return output, nil
}

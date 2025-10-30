// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"errors"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
)

// Common base for all extractors that provides some functionality
// that would otherwise be duplicated across all extractors.
type BaseExtractor[Opts any, Feature db.Table] struct {
	// Options to pass via yaml to this step.
	libconf.JsonOpts[Opts]
	// Database connection where the datasources are stored.
	DB *db.DB
	// Database connection where the features will be stored.
	// TODO: Remove this once we don't fetch features from the DB anymore.
	extractorDB    *db.DB
	RecencySeconds int
	UpdatedAt      *time.Time
}

// Init the extractor with the database and options.
func (e *BaseExtractor[Opts, Feature]) Init(datasourceDB, extractorDB *db.DB, spec v1alpha1.KnowledgeSpec) error {
	rawOpts := libconf.NewRawOpts(`{}`)
	if len(spec.Extractor.Config.Raw) > 0 {
		rawOpts = libconf.NewRawOptsBytes(spec.Extractor.Config.Raw)
	}
	if err := e.Load(rawOpts); err != nil {
		return err
	}
	e.DB = datasourceDB
	e.RecencySeconds = 0
	if int(spec.Recency.Seconds()) != 0 {
		e.RecencySeconds = int(spec.Recency.Seconds())
	}
	var f Feature
	// TODO: Remove this once we don't fetch features from the DB anymore.
	if extractorDB != nil {
		if err := extractorDB.CreateTable(extractorDB.AddTable(f)); err != nil {
			return err
		}
	}
	e.extractorDB = extractorDB
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
	// TODO: Remove this once we don't fetch features from the DB anymore.
	if e.extractorDB != nil {
		if err := db.ReplaceAll(*e.extractorDB, fs...); err != nil {
			return nil, err
		}
	}
	output := make([]Feature, len(fs))
	for i, f := range fs {
		output[i] = f
	}
	var model F
	slog.Info("features: extracted", model.TableName(), len(output))
	return output, nil
}

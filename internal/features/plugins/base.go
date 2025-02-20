// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Common base for all extractors that provides some functionality
// that would otherwise be duplicated across all extractors.
type BaseExtractor[Opts any, Feature db.Table] struct {
	// Options to pass via yaml to this step.
	conf.YamlOpts[Opts]
	// Database connection.
	DB db.DB
}

// Init the extractor with the database and options.
func (e *BaseExtractor[Opts, Feature]) Init(db db.DB, opts conf.RawOpts) error {
	if err := e.YamlOpts.Load(opts); err != nil {
		return err
	}
	e.DB = db
	var f Feature
	return db.CreateTable(db.AddTable(f))
}

// Replace all features of the given model in the database and
// return them as a slice of generic features for counting.
func (e *BaseExtractor[Opts, F]) Extracted(fs []F) ([]Feature, error) {
	if err := db.ReplaceAll(e.DB, fs...); err != nil {
		return nil, err
	}
	output := make([]Feature, len(fs))
	for i, f := range fs {
		output[i] = f
	}
	var model F
	slog.Info("features: extracted", model.TableName(), len(output))
	return output, nil
}

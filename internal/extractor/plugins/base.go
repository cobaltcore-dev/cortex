// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Common base for all extractors that provides some functionality
// that would otherwise be duplicated across all extractors.
type BaseExtractor[Opts any, Feature db.Table] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// Database connection.
	DB             db.DB
	RecencySeconds int
	UpdatedAt      *time.Time
}

// Init the extractor with the database and options.
func (e *BaseExtractor[Opts, Feature]) Init(db db.DB, conf conf.FeatureExtractorConfig) error {
	if err := e.Load(conf.Options); err != nil {
		return err
	}
	e.DB = db
	e.RecencySeconds = 0
	if conf.RecencySeconds != nil {
		e.RecencySeconds = *conf.RecencySeconds
	}
	var f Feature
	return db.CreateTable(db.AddTable(f))
}

// Extract the features directly from an sql query.
func (e *BaseExtractor[Opts, F]) ExtractSQL(query string) ([]Feature, error) {
	var features []F
	if _, err := e.DB.Select(&features, query); err != nil {
		return nil, err
	}
	return e.Extracted(features)
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

// Checks if the last update of the extractor is older than the configured recency.
// If the recency is set to a positive value, it will return true if the last update
// is older than the configured recency in seconds.
func (e *BaseExtractor[Opts, F]) NeedsUpdate() bool {
	// UpdateAt is nil if the extractor has never been run.
	if e.UpdatedAt == nil {
		return true
	}
	if e.RecencySeconds <= 0 {
		return true
	}
	return time.Since(*e.UpdatedAt) > time.Duration(e.RecencySeconds)*time.Second
}

// Mark the extractor as updated by setting the UpdatedAt field to the current time.
func (e *BaseExtractor[Opts, F]) MarkAsUpdated() {
	time := time.Now()
	e.UpdatedAt = &time
}

func (e *BaseExtractor[Opts, F]) NextPossibleExecution() time.Time {
	if e.RecencySeconds <= 0 {
		return time.Time{}
	}
	if e.UpdatedAt == nil {
		return time.Now()
	}
	return e.UpdatedAt.Add(time.Duration(e.RecencySeconds) * time.Second)
}

func (e *BaseExtractor[Opts, F]) NotifySkip() {
	// Currently only needed for the feature extractor monitor, to count skips.
}

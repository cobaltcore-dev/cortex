// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"gopkg.in/yaml.v2"
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
func (s *BaseExtractor[Opts, Feature]) Init(db db.DB, opts yaml.MapSlice) error {
	if err := s.YamlOpts.Load(opts); err != nil {
		return err
	}
	s.DB = db
	var f Feature
	return db.CreateTable(db.AddTable(f))
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Common base for all steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseStep[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// Database connection.
	DB db.DB
}

// Init the step with the database and options.
func (s *BaseStep[Opts]) Init(db db.DB, opts conf.RawOpts) error {
	if err := s.Load(opts); err != nil {
		return err
	}
	s.DB = db
	return nil
}

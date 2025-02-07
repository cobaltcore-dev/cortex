// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/go-pg/pg/v10/orm"
	"gopkg.in/yaml.v2"
)

// Mixin that can be embedded in a step to provide some common tooling.
type BaseExtractor[Table any, Options any] struct {
	DB      db.DB
	Table   Table
	Options Options
}

func (e *BaseExtractor[Table, Options]) Init(db db.DB, opts yaml.MapSlice) error {
	e.DB = db
	if err := e.DB.Get().Model((*Table)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return e.LoadOpts(opts)
}

// Set the options contained in the opts yaml map.
func (s *BaseExtractor[Table, Options]) LoadOpts(opts yaml.MapSlice) error {
	bytes, err := yaml.Marshal(opts)
	if err != nil {
		return err
	}
	var o Options
	if err := yaml.UnmarshalStrict(bytes, &o); err != nil {
		return err
	}
	s.Options = o
	return nil
}

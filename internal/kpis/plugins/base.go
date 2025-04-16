// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Common base for all KPIs that provides some functionality
// that would otherwise be duplicated across all KPIs.
type BaseKPI[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.YamlOpts[Opts]
	// Database connection.
	DB db.DB
}

// Init the KPI with the database, options, and the registry to publish metrics on.
func (k *BaseKPI[Opts]) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.Load(opts); err != nil {
		return err
	}
	k.DB = db
	return nil
}

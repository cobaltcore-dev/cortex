// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all KPIs that provides some functionality
// that would otherwise be duplicated across all KPIs.
type BaseKPI[Opts any] struct {
	// Options to pass via json to this step.
	conf.JsonOpts[Opts]
	// (Optional) database connection where datasources are stored.
	DB *db.DB
	// Kubernetes client to access other resources.
	Client client.Client
}

// Init the KPI with the database, options, and the registry to publish metrics on.
func (k *BaseKPI[Opts]) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.Load(opts); err != nil {
		return err
	}
	k.DB = db
	k.Client = client
	return nil
}

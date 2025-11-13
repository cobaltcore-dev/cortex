// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import "context"

// Common interface for data sources.
type Datasource interface {
	// Initialize the data source, e.g. create database tables.
	Init(context.Context)
	// Download data from the data source.
	Sync(context.Context)
}

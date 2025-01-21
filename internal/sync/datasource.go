// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sync

// Common interface for data sources.
type Datasource interface {
	// Initialize the data source, e.g. create database tables.
	Init()
	// Download data from the data source.
	Sync()
}

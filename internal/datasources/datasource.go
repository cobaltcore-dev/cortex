// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

type Datasource interface {
	Init()
	Sync()
}

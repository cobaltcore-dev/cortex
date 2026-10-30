// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

// Index of all supported kpis, keyed by name.
// Each kpi registers itself here through a func init() in its own file.
var Index = map[string]func() KPI{}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
)

type Config struct {
	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`

	// Lib modules configs.
	libconf.MonitoringConfig `json:"monitoring"`
}

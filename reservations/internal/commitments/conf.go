// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import "github.com/cobaltcore-dev/cortex/lib/conf"

// Configuration for the commitments module.
type Config struct {
	// Keystone config.
	Keystone conf.KeystoneConfig `json:"keystone"`
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

type Config struct {
	// The operator will only touch CRs with this operator name.
	Operator string `json:"operator"`
}

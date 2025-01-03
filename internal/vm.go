// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package internal

type VM struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Image  string `json:"image"`
	Flavor string `json:"flavor"`
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package internal

type Hypervisor struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	State string `json:"state"`
}

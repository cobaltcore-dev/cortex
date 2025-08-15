// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1

type Host struct {
	// The kind of host, e.g. "compute"
	Kind string `json:"kind"`
	// The name of the host, e.g. "host-1"
	Name string `json:"name"`
}

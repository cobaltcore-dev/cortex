// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps long term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionLongTerm struct {
	ComputeHost      string
	AvgCPUContention float64
	MaxCPUContention float64
}

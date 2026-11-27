// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps short term CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTerm struct {
	ComputeHost      string
	AvgCPUContention float64
	MaxCPUContention float64
}

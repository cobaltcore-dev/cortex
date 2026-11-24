// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

// Inputs of the vROps hostsystem contention extractors.
type VROpsHostsystemContentionInputs struct {
	// The vROps hostsystem cpu contention metrics.
	Metrics []prometheus.VROpsHostMetric `input:"metrics"`
	// Features of the vROps hostsystem resolver knowledge.
	ResolvedHostsystems []ResolvedVROpsHostsystem `input:"resolvedHostsystems"`
}

// CPU contention of a compute host.
type hostContention struct {
	computeHost      string
	avgCPUContention float64
	maxCPUContention float64
}

// Calculate the cpu contention of each compute host. The average and maximum
// contention is first calculated per hostsystem, then hostsystems are resolved
// to compute hosts, which get the average of the averages and the maximum of
// the maximums of their hostsystems.
func hostContentions(in VROpsHostsystemContentionInputs) []hostContention {
	type stats struct {
		sum, max float64
		n        int
	}
	byHostsystem := make(map[string]*stats)
	for _, m := range in.Metrics {
		s, ok := byHostsystem[m.HostSystem]
		if !ok {
			byHostsystem[m.HostSystem] = &stats{sum: m.Value, max: m.Value, n: 1}
			continue
		}
		s.sum += m.Value
		s.max = max(s.max, m.Value)
		s.n++
	}
	computeHostByHostsystem := make(map[string]string)
	for _, rh := range in.ResolvedHostsystems {
		computeHostByHostsystem[rh.VROpsHostsystem] = rh.NovaComputeHost
	}
	type hostStats struct {
		sumOfAvgs, max float64
		n              int
	}
	byComputeHost := make(map[string]*hostStats)
	for hostsystem, s := range byHostsystem {
		computeHost, ok := computeHostByHostsystem[hostsystem]
		if !ok {
			log.V(1).Info("skipping unresolved hostsystem", "hostsystem", hostsystem)
			continue
		}
		hs, ok := byComputeHost[computeHost]
		if !ok {
			hs = &hostStats{}
			byComputeHost[computeHost] = hs
		}
		hs.sumOfAvgs += s.sum / float64(s.n)
		hs.max = max(hs.max, s.max)
		hs.n++
	}
	contentions := make([]hostContention, 0, len(byComputeHost))
	for computeHost, hs := range byComputeHost {
		contentions = append(contentions, hostContention{
			computeHost:      computeHost,
			avgCPUContention: hs.sumOfAvgs / float64(hs.n),
			maxCPUContention: hs.max,
		})
	}
	slices.SortFunc(contentions, func(a, b hostContention) int {
		return strings.Compare(a.computeHost, b.computeHost)
	})
	return contentions
}

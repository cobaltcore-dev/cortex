// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps a vROps hostsystem to a nova compute host.
type ResolvedVROpsHostsystem struct {
	VROpsHostsystem string `db:"vrops_hostsystem"`
	NovaComputeHost string `db:"nova_compute_host"`
}

// Inputs of the vROps hostsystem resolver.
type VROpsHostsystemResolverInputs struct {
	// Any vROps virtual machine metric.
	Metrics []prometheus.VROpsVMMetric `input:"metrics"`
	Servers []nova.Server              `input:"servers"`
}

// Extractor that resolves the vROps hostsystem to nova compute hosts.
type VROpsHostsystemResolver struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Resolve the hostsystem names from vROps vm metrics to the compute hosts of
// the nova servers the metrics belong to.
func (e *VROpsHostsystemResolver) Extract(_ context.Context, in VROpsHostsystemResolverInputs) ([]ResolvedVROpsHostsystem, error) {
	hostByServer := make(map[string]string, len(in.Servers))
	for _, s := range in.Servers {
		hostByServer[s.ID] = s.OSEXTSRVATTRHost
	}
	resolved := make(map[ResolvedVROpsHostsystem]struct{})
	for _, m := range in.Metrics {
		host, ok := hostByServer[m.InstanceUUID]
		if !ok || host == "" {
			continue
		}
		resolved[ResolvedVROpsHostsystem{VROpsHostsystem: m.HostSystem, NovaComputeHost: host}] = struct{}{}
	}
	features := make([]ResolvedVROpsHostsystem, 0, len(resolved))
	for f := range resolved {
		features = append(features, f)
	}
	slices.SortFunc(features, func(a, b ResolvedVROpsHostsystem) int {
		return cmp.Or(cmp.Compare(a.VROpsHostsystem, b.VROpsHostsystem), cmp.Compare(a.NovaComputeHost, b.NovaComputeHost))
	})
	return features, nil
}

func init() {
	extractor.Register("vrops_hostsystem_resolver", func() extractor.Extractor[VROpsHostsystemResolverInputs, ResolvedVROpsHostsystem] {
		return &VROpsHostsystemResolver{}
	})
}

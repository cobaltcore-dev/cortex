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

// Feature that maps the maximum cpu steal time of a virtual machine.
type LibvirtDomainCPUStealPct struct {
	// The openstack server instance uuid.
	InstanceUUID string `db:"instance_uuid"`
	// The compute host on which the instance is running.
	Host string `db:"host"`
	// The maximum steal pct recorded.
	MaxStealTimePct float64 `db:"max_steal_time_pct"`
}

// Inputs of the libvirt domain cpu steal pct extractor.
type LibvirtDomainCPUStealPctInputs struct {
	// Steal time metrics of libvirt domains.
	Metrics []prometheus.KVMDomainMetric `input:"metrics"`
	Servers []nova.Server                `input:"servers"`
}

// Extractor that extracts the cpu steal time of kvm virtual machines.
type LibvirtDomainCPUStealPctExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the maximum steal time of each server, matching the libvirt domain
// of the metrics with the instance name of the server.
func (e *LibvirtDomainCPUStealPctExtractor) Extract(_ context.Context, in LibvirtDomainCPUStealPctInputs) ([]LibvirtDomainCPUStealPct, error) {
	serversByInstanceName := make(map[string][]nova.Server)
	for _, s := range in.Servers {
		if s.ID == "" {
			continue
		}
		serversByInstanceName[s.OSEXTSRVATTRInstanceName] = append(serversByInstanceName[s.OSEXTSRVATTRInstanceName], s)
	}
	type key struct{ instanceUUID, host string }
	maxByServer := make(map[key]float64)
	for _, m := range in.Metrics {
		for _, s := range serversByInstanceName[m.Domain] {
			k := key{s.ID, s.OSEXTSRVATTRHost}
			if current, ok := maxByServer[k]; !ok || m.Value > current {
				maxByServer[k] = m.Value
			}
		}
	}
	features := make([]LibvirtDomainCPUStealPct, 0, len(maxByServer))
	for k, v := range maxByServer {
		features = append(features, LibvirtDomainCPUStealPct{InstanceUUID: k.instanceUUID, Host: k.host, MaxStealTimePct: v})
	}
	slices.SortFunc(features, func(a, b LibvirtDomainCPUStealPct) int {
		return cmp.Or(cmp.Compare(a.InstanceUUID, b.InstanceUUID), cmp.Compare(a.Host, b.Host))
	})
	return features, nil
}

func init() {
	extractor.Register("kvm_libvirt_domain_cpu_steal_pct_extractor", func() extractor.Extractor[LibvirtDomainCPUStealPctInputs, LibvirtDomainCPUStealPct] {
		return &LibvirtDomainCPUStealPctExtractor{}
	})
}

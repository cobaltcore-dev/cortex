// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"cmp"
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps the cpu usage of a storage pool.
type StoragePoolCPUUsage struct {
	// Name of the OpenStack storage pool.
	StoragePoolName string `db:"storage_pool_name"`
	// Avg CPU usage in pct.
	AvgCPUUsagePct float64 `db:"avg_cpu_usage_pct"`
	// Max CPU usage in pct.
	MaxCPUUsagePct float64 `db:"max_cpu_usage_pct"`
}

// Inputs of the storage pool cpu usage extractor.
type StoragePoolCPUUsageInputs struct {
	StoragePools []manila.StoragePool `input:"storagePools"`
	// NetApp aggregate label metrics, mapping aggregates to nodes.
	AggregateLabels []prometheus.NetAppAggregateLabelsMetric `input:"aggregateLabels"`
	// NetApp node cpu busy metrics.
	NodeCPUBusy []prometheus.NetAppNodeMetric `input:"nodeCPUBusy"`
}

// Extractor that extracts the CPU usage of a storage pool.
type StoragePoolCPUUsageExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the CPU usage of the netapp nodes backing each manila storage pool.
// The pool of a manila storage pool is the name of the netapp aggregate, which
// may span multiple nodes, resulting in one feature per pool and node.
func (e *StoragePoolCPUUsageExtractor) Extract(_ context.Context, in StoragePoolCPUUsageInputs) ([]StoragePoolCPUUsage, error) {
	nodesByAggregate := make(map[string][]string)
	for _, l := range in.AggregateLabels {
		if !slices.Contains(nodesByAggregate[l.Aggregate], l.Node) {
			nodesByAggregate[l.Aggregate] = append(nodesByAggregate[l.Aggregate], l.Node)
		}
	}
	type stats struct {
		sum, max float64
		n        int
	}
	byNode := make(map[string]*stats)
	for _, m := range in.NodeCPUBusy {
		s, ok := byNode[m.Node]
		if !ok {
			byNode[m.Node] = &stats{sum: m.Value, max: m.Value, n: 1}
			continue
		}
		s.sum += m.Value
		s.max = max(s.max, m.Value)
		s.n++
	}
	var features []StoragePoolCPUUsage
	for _, pool := range in.StoragePools {
		for _, node := range nodesByAggregate[pool.Pool] {
			s, ok := byNode[node]
			if !ok {
				continue
			}
			features = append(features, StoragePoolCPUUsage{
				StoragePoolName: pool.Name,
				AvgCPUUsagePct:  s.sum / float64(s.n),
				MaxCPUUsagePct:  s.max,
			})
		}
	}
	slices.SortFunc(features, func(a, b StoragePoolCPUUsage) int {
		return cmp.Or(
			cmp.Compare(a.StoragePoolName, b.StoragePoolName),
			cmp.Compare(a.AvgCPUUsagePct, b.AvgCPUUsagePct),
			cmp.Compare(a.MaxCPUUsagePct, b.MaxCPUUsagePct),
		)
	})
	return features, nil
}

func init() {
	extractor.Register("netapp_storage_pool_cpu_usage_extractor", func() extractor.Extractor[StoragePoolCPUUsageInputs, StoragePoolCPUUsage] {
		return &StoragePoolCPUUsageExtractor{}
	})
}

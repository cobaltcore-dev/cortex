// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestStoragePoolCPUUsageExtractor_Extract(t *testing.T) {
	in := StoragePoolCPUUsageInputs{
		StoragePools: []manila.StoragePool{
			{Name: "pool2", Pool: "aggr2"},
			{Name: "pool1", Pool: "aggr1"},
			// Aggregate spanning multiple nodes yields one feature per node.
			{Name: "pool3", Pool: "aggr3"},
			// Pools without aggregate labels are skipped.
			{Name: "pool4", Pool: "aggr4"},
		},
		AggregateLabels: []prometheus.NetAppAggregateLabelsMetric{
			{Aggregate: "aggr1", Node: "node1"},
			{Aggregate: "aggr2", Node: "node2"},
			{Aggregate: "aggr3", Node: "node1"},
			{Aggregate: "aggr3", Node: "node2"},
			// Duplicate labels are ignored.
			{Aggregate: "aggr3", Node: "node2"},
		},
		NodeCPUBusy: []prometheus.NetAppNodeMetric{
			{Node: "node1", Value: 10.5},
			{Node: "node1", Value: 20.0},
			{Node: "node2", Value: 30.0},
			{Node: "node2", Value: 40.0},
		},
	}
	features, err := (&StoragePoolCPUUsageExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []StoragePoolCPUUsage{
		{StoragePoolName: "pool1", AvgCPUUsagePct: 15.25, MaxCPUUsagePct: 20.0},
		{StoragePoolName: "pool2", AvgCPUUsagePct: 35.0, MaxCPUUsagePct: 40.0},
		{StoragePoolName: "pool3", AvgCPUUsagePct: 15.25, MaxCPUUsagePct: 20.0},
		{StoragePoolName: "pool3", AvgCPUUsagePct: 35.0, MaxCPUUsagePct: 40.0},
	}
	if !slices.Equal(features, expected) {
		t.Errorf("expected %+v, got %+v", expected, features)
	}
}

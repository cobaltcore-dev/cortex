// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestLibvirtDomainCPUStealPctExtractor_Extract(t *testing.T) {
	in := LibvirtDomainCPUStealPctInputs{
		Metrics: []prometheus.KVMDomainMetric{
			{Domain: "instance-00000001", Value: 15.5},
			{Domain: "instance-00000001", Value: 25.0},
			{Domain: "instance-00000002", Value: 10.2},
			{Domain: "instance-00000003", Value: 30.8},
			// Metrics without matching server are skipped.
			{Domain: "instance-00000004", Value: 50.0},
		},
		Servers: []nova.Server{
			{ID: "uuid-3", OSEXTSRVATTRInstanceName: "instance-00000003", OSEXTSRVATTRHost: "compute-host-1"},
			{ID: "uuid-1", OSEXTSRVATTRInstanceName: "instance-00000001", OSEXTSRVATTRHost: "compute-host-1"},
			{ID: "uuid-2", OSEXTSRVATTRInstanceName: "instance-00000002", OSEXTSRVATTRHost: "compute-host-2"},
		},
	}
	features, err := (&LibvirtDomainCPUStealPctExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []LibvirtDomainCPUStealPct{
		{InstanceUUID: "uuid-1", Host: "compute-host-1", MaxStealTimePct: 25.0},
		{InstanceUUID: "uuid-2", Host: "compute-host-2", MaxStealTimePct: 10.2},
		{InstanceUUID: "uuid-3", Host: "compute-host-1", MaxStealTimePct: 30.8},
	}
	if !slices.Equal(features, expected) {
		t.Errorf("expected %+v, got %+v", expected, features)
	}
}

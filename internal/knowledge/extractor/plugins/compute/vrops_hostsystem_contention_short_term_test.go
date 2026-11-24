// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestVROpsHostsystemContentionShortTermExtractor_Extract(t *testing.T) {
	in := VROpsHostsystemContentionInputs{
		Metrics: []prometheus.VROpsHostMetric{
			{HostSystem: "hostsystem1", Value: 30.0},
			{HostSystem: "hostsystem2", Value: 40.0},
			{HostSystem: "hostsystem1", Value: 50.0},
			{HostSystem: "unresolved", Value: 99.0},
		},
		ResolvedHostsystems: []ResolvedVROpsHostsystem{
			{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host2"},
			{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
		},
	}
	features, err := (&VROpsHostsystemContentionShortTermExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []VROpsHostsystemContentionShortTerm{
		{ComputeHost: "compute_host1", AvgCPUContention: 40.0, MaxCPUContention: 50.0},
		{ComputeHost: "compute_host2", AvgCPUContention: 40.0, MaxCPUContention: 40.0},
	}
	if !slices.Equal(features, expected) {
		t.Errorf("expected %+v, got %+v", expected, features)
	}
}

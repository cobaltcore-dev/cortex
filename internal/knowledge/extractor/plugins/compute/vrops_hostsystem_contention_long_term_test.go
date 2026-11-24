// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
)

func TestVROpsHostsystemContentionLongTermExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		in       VROpsHostsystemContentionInputs
		expected []VROpsHostsystemContentionLongTerm
	}{
		{
			name: "average and maximum per compute host",
			in: VROpsHostsystemContentionInputs{
				Metrics: []prometheus.VROpsHostMetric{
					{HostSystem: "hostsystem1", Value: 30.0},
					{HostSystem: "hostsystem2", Value: 40.0},
					{HostSystem: "hostsystem1", Value: 50.0},
				},
				ResolvedHostsystems: []ResolvedVROpsHostsystem{
					{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
					{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host2"},
				},
			},
			expected: []VROpsHostsystemContentionLongTerm{
				{ComputeHost: "compute_host1", AvgCPUContention: 40.0, MaxCPUContention: 50.0},
				{ComputeHost: "compute_host2", AvgCPUContention: 40.0, MaxCPUContention: 40.0},
			},
		},
		{
			name: "average of averages and maximum of maximums across hostsystems, unresolved skipped",
			in: VROpsHostsystemContentionInputs{
				Metrics: []prometheus.VROpsHostMetric{
					{HostSystem: "hostsystem1", Value: 30.0},
					{HostSystem: "hostsystem1", Value: 50.0},
					{HostSystem: "hostsystem2", Value: 10.0},
					{HostSystem: "unresolved", Value: 99.0},
				},
				ResolvedHostsystems: []ResolvedVROpsHostsystem{
					{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
					{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host1"},
				},
			},
			expected: []VROpsHostsystemContentionLongTerm{
				// avg((30+50)/2, 10) = 25, max(50, 10) = 50
				{ComputeHost: "compute_host1", AvgCPUContention: 25.0, MaxCPUContention: 50.0},
			},
		},
		{
			name: "no resolved hostsystems",
			in: VROpsHostsystemContentionInputs{
				Metrics: []prometheus.VROpsHostMetric{{HostSystem: "hostsystem1", Value: 30.0}},
			},
			expected: []VROpsHostsystemContentionLongTerm{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, err := (&VROpsHostsystemContentionLongTermExtractor{}).Extract(t.Context(), tt.in)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !slices.Equal(features, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, features)
			}
		})
	}
}

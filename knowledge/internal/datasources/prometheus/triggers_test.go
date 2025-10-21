// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"testing"
)

func TestTriggerMetricAliasSynced(t *testing.T) {
	tests := []struct {
		name        string
		metricAlias string
		expected    string
	}{
		{
			name:        "basic alias",
			metricAlias: "cpu_usage",
			expected:    "triggers/sync/prometheus/alias/cpu_usage",
		},
		{
			name:        "empty alias",
			metricAlias: "",
			expected:    "triggers/sync/prometheus/alias/",
		},
		{
			name:        "complex alias",
			metricAlias: "vrops_host_memory_utilization",
			expected:    "triggers/sync/prometheus/alias/vrops_host_memory_utilization",
		},
		{
			name:        "alias with special characters",
			metricAlias: "test-metric_123",
			expected:    "triggers/sync/prometheus/alias/test-metric_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TriggerMetricAliasSynced(tt.metricAlias)
			if result != tt.expected {
				t.Errorf("TriggerMetricAliasSynced(%q) = %q, expected %q", tt.metricAlias, result, tt.expected)
			}
		})
	}
}

func TestTriggerMetricTypeSynced(t *testing.T) {
	tests := []struct {
		name       string
		metricType string
		expected   string
	}{
		{
			name:       "basic type",
			metricType: "node_exporter_metric",
			expected:   "triggers/sync/prometheus/type/node_exporter_metric",
		},
		{
			name:       "empty type",
			metricType: "",
			expected:   "triggers/sync/prometheus/type/",
		},
		{
			name:       "vrops host metric type",
			metricType: "vrops_host_metric",
			expected:   "triggers/sync/prometheus/type/vrops_host_metric",
		},
		{
			name:       "kvm domain metric type",
			metricType: "kvm_libvirt_domain_metric",
			expected:   "triggers/sync/prometheus/type/kvm_libvirt_domain_metric",
		},
		{
			name:       "type with special characters",
			metricType: "test-type_123",
			expected:   "triggers/sync/prometheus/type/test-type_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TriggerMetricTypeSynced(tt.metricType)
			if result != tt.expected {
				t.Errorf("TriggerMetricTypeSynced(%q) = %q, expected %q", tt.metricType, result, tt.expected)
			}
		})
	}
}

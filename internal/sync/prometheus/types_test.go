// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"testing"
	"time"
)

func TestVROpsHostMetric(t *testing.T) {
	metric := VROpsHostMetric{
		Name:         "cpu_usage",
		Cluster:      "cluster1",
		ClusterType:  "type1",
		Collector:    "collector1",
		Datacenter:   "datacenter1",
		HostSystem:   "host1",
		InternalName: "internal1",
		Job:          "job1",
		Prometheus:   "prometheus1",
		Region:       "region1",
		VCCluster:    "vccluster1",
		VCenter:      "vcenter1",
		Timestamp:    time.Now(),
		Value:        0.5,
	}

	if metric.GetName() != "cpu_usage" {
		t.Errorf("expected name to be 'cpu_usage', got %s", metric.GetName())
	}

	if metric.GetTimestamp().IsZero() {
		t.Error("expected timestamp to be set")
	}

	newMetric := metric.With(time.Unix(0, 0), 1.0)
	if !newMetric.GetTimestamp().Equal(time.Unix(0, 0)) {
		t.Errorf("expected timestamp to be '1970-01-01 00:00:00 +0000 UTC', got %s", metric.GetTimestamp())
	}
	if newMetric.GetValue() != 1.0 {
		t.Errorf("expected value to be 1.0, got %f", metric.GetValue())
	}
}

func TestVROpsVMMetric(t *testing.T) {
	metric := VROpsVMMetric{
		Name:           "cpu_usage",
		Cluster:        "cluster1",
		ClusterType:    "type1",
		Collector:      "collector1",
		Datacenter:     "datacenter1",
		HostSystem:     "host1",
		InternalName:   "internal1",
		Job:            "job1",
		Project:        "project1",
		Prometheus:     "prometheus1",
		Region:         "region1",
		VCCluster:      "vccluster1",
		VCenter:        "vcenter1",
		VirtualMachine: "vm1",
		InstanceUUID:   "uuid1",
		Timestamp:      time.Now(),
		Value:          0.5,
	}

	if metric.GetName() != "cpu_usage" {
		t.Errorf("expected name to be 'cpu_usage', got %s", metric.GetName())
	}

	if metric.GetTimestamp().IsZero() {
		t.Error("expected timestamp to be set")
	}

	newMetric := metric.With(time.Unix(0, 0), 1.0)
	if !newMetric.GetTimestamp().Equal(time.Unix(0, 0)) {
		t.Errorf("expected timestamp to be '1970-01-01 00:00:00 +0000 UTC', got %s", metric.GetTimestamp())
	}
	if newMetric.GetValue() != 1.0 {
		t.Errorf("expected value to be 1.0, got %f", metric.GetValue())
	}
}

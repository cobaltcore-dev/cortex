// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestHostRunningVMsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostRunningVMsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostRunningVMsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(sap.HostDetails{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hypervisors := []any{
		&sap.HostDetails{
			ComputeHost:      "host1",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
		},
		// Should be ignored since its an ironic host
		&sap.HostDetails{
			ComputeHost:      "host2",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostRunningVMsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type HostRunningVMsMetric struct {
		ComputeHost      string
		AvailabilityZone string
		Enabled          string
		CPUArchitecture  string
		WorkloadType     string
		HypervisorFamily string
		Value            float64
	}

	actualMetrics := make(map[string]HostRunningVMsMetric, 0)

	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["compute_host"]

		actualMetrics[key] = HostRunningVMsMetric{
			ComputeHost:      labels["compute_host"],
			AvailabilityZone: labels["availability_zone"],
			Enabled:          labels["enabled"],
			CPUArchitecture:  labels["cpu_architecture"],
			WorkloadType:     labels["workload_type"],
			HypervisorFamily: labels["hypervisor_family"],
			Value:            m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]HostRunningVMsMetric{
		"host1": {
			ComputeHost:      "host1",
			AvailabilityZone: "az1",
			Enabled:          "true",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			HypervisorFamily: "vmware",
			Value:            5,
		},
	}

	if len(expectedMetrics) != len(actualMetrics) {
		t.Errorf("expected %d metrics, got %d", len(expectedMetrics), len(actualMetrics))
	}

	for key, expected := range expectedMetrics {
		actual, ok := actualMetrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}

		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

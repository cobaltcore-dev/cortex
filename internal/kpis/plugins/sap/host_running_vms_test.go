// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
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
		testDB.AddTable(shared.HostDomainProject{}),
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

	expectedLabels := map[string]map[string]string{
		"host1": {
			"compute_host":      "host1",
			"availability_zone": "az1",
			"enabled":           "true",
			"cpu_architecture":  "cascade-lake",
			"workload_type":     "general-purpose",
			"hypervisor_family": "vmware",
		},
	}

	expectedValues := map[string]float64{
		"host1": 5,
	}

	metricsCount := 0

	for metric := range ch {
		metricsCount++
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		expectedValue := m.Gauge.GetValue()

		computeHost := labels["compute_host"]

		if value, exists := expectedValues[computeHost]; exists {
			if value != expectedValue {
				t.Errorf("expected value %f for host %s, got %f", value, computeHost, expectedValue)
			}
		} else {
			t.Errorf("unexpected compute host %s in metric labels", computeHost)
		}

		if expected, ok := expectedLabels[computeHost]; ok {
			for key, expectedValue := range expected {
				if value, exists := labels[key]; !exists || value != expectedValue {
					t.Errorf("expected label %s to be %s for host %s, got %s", key, expectedValue, computeHost, value)
				}
			}
		} else {
			t.Errorf("unexpected compute host %s in metric labels", computeHost)
		}
	}

	if metricsCount != 1 {
		t.Errorf("expected one metric, got %d", metricsCount)
	}
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"slices"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestHostTotalCapacityKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostTotalCapacityKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostTotalCapacityKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(sap.HostDetails{}),
		testDB.AddTable(shared.HostUtilization{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hypervisors := []any{
		&sap.HostDetails{
			ComputeHost:      "vwmare-host",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			RunningVMs:       5,
			WorkloadType:     "general-purpose",
			Enabled:          true,
		},
		&sap.HostDetails{
			ComputeHost:      "kvm-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			RunningVMs:       5,
			WorkloadType:     "hana",
			Enabled:          false,
		},
		&sap.HostDetails{
			ComputeHost:      "ironic-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "kvm",
			RunningVMs:       5,
			WorkloadType:     "hana",
			Enabled:          false,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations := []any{
		&shared.HostUtilization{
			ComputeHost:      "vwmare-host",
			RAMUtilizedPct:   50,
			VCPUsUtilizedPct: 50,
			DiskUtilizedPct:  50,
			// Assuimg 100 <unit> for every resource so we don't have to write an extra expected model for each resource
			TotalRAMAllocatableMB:  100,
			TotalVCPUsAllocatable:  100,
			TotalDiskAllocatableGB: 100,
		},
		&shared.HostUtilization{
			ComputeHost:      "kvm-host",
			RAMUtilizedPct:   80,
			VCPUsUtilizedPct: 75,
			DiskUtilizedPct:  80,
			// Assuimg 1000 <unit> for every resource so we don't have to write an extra expected model for each resource
			TotalRAMAllocatableMB:  1000,
			TotalVCPUsAllocatable:  1000,
			TotalDiskAllocatableGB: 1000,
		},
		&shared.HostUtilization{
			ComputeHost:            "ironic-host",
			RAMUtilizedPct:         0,
			VCPUsUtilizedPct:       0,
			DiskUtilizedPct:        0,
			TotalRAMAllocatableMB:  0,
			TotalVCPUsAllocatable:  0,
			TotalDiskAllocatableGB: 0,
		},
	}

	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostTotalCapacityKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0

	// Used to track if for each host a "ram", "cpu", and "disk" metric is present
	// (ignoring the histogram metric)
	metricsUtilizationResourceLabels := make(map[string][]string)
	metricsUtilizationResourceLabels["vwmare-host"] = make([]string, 0)
	metricsUtilizationResourceLabels["kvm-host"] = make([]string, 0)
	metricsUtilizationResourceLabels["ironic-host"] = make([]string, 0)

	// Expected resource labels for each host
	expectedMetricUtilizationResourceLabels := map[string][]string{
		"vwmare-host": {"ram", "cpu", "disk"},
		"kvm-host":    {"ram", "cpu", "disk"},
		"ironic-host": {}, // Ironic host are filtered out
	}

	// Used to track for each host the expected labels
	// Note: Doesn't include the "resource" label since there are multiple resources per host
	// The resource label is tracked in metricsUtilizationResourceLabels
	expectedLabels := map[string]map[string]string{
		"vwmare-host": {
			"compute_host":      "vwmare-host",
			"availability_zone": "az1",
			"enabled":           "true",
			"cpu_architecture":  "cascade-lake",
			"workload_type":     "general-purpose",
			"hypervisor_family": "vmware",
		},
		"kvm-host": {
			"compute_host":      "kvm-host",
			"availability_zone": "az2",
			"enabled":           "false",
			"cpu_architecture":  "cascade-lake",
			"workload_type":     "hana",
			"hypervisor_family": "kvm",
		},
	}

	for metric := range ch {
		metricName := metric.Desc().String()
		// Ignore the histogram metric in this test
		if strings.Contains(metricName, "cortex_sap_host_utilization_pct") {
			continue
		}

		metricsCount++
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		computeHost := labels["compute_host"]

		metricsUtilizationResourceLabels[computeHost] = append(metricsUtilizationResourceLabels[computeHost], labels["resource"])

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

	for host, resources := range metricsUtilizationResourceLabels {
		if len(resources) != len(expectedMetricUtilizationResourceLabels[host]) {
			t.Errorf("expected %d resources for host %s, got %d", len(expectedMetricUtilizationResourceLabels[host]), host, len(resources))
		}
		for _, resource := range resources {
			if !slices.Contains(expectedMetricUtilizationResourceLabels[host], resource) {
				t.Errorf("unexpected resource %s for host %s", resource, host)
			}
		}
	}
}

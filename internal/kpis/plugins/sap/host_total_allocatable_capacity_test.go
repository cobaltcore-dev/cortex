// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestHostTotalAllocatableCapacityKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostTotalAllocatableCapacityKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostTotalAllocatableCapacityKPI_Collect(t *testing.T) {
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
			ComputeHost:      "vmware-host",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			PinnedProjects:   testlib.Ptr("project-123,project-456"),
		},
		&sap.HostDetails{
			ComputeHost:      "kvm-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			PinnedProjects:   nil,
		},
		&sap.HostDetails{
			ComputeHost:      "ironic-host",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
		},
		// Skip this host as it has no usage data
		&sap.HostDetails{
			ComputeHost:      "kvm-host-2",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "hana",
			Enabled:          false,
			PinnedProjects:   nil,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations := []any{
		&shared.HostUtilization{
			ComputeHost:            "vmware-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  200,
			TotalDiskAllocatableGB: 300,
		},
		&shared.HostUtilization{
			ComputeHost:            "kvm-host",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  100,
			TotalDiskAllocatableGB: 100,
		},
		&shared.HostUtilization{
			ComputeHost:            "ironic-host",
			TotalVCPUsAllocatable:  0,
			TotalRAMAllocatableMB:  0,
			TotalDiskAllocatableGB: 0,
		},
		// No usage data for kvm-host-2
	}

	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostTotalAllocatableCapacityKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type HostResourceMetric struct {
		ComputeHost      string
		Resource         string
		AvailabilityZone string
		Enabled          string
		CPUArchitecture  string
		WorkloadType     string
		HypervisorFamily string
		PinnedProjects   string
		Value            float64
	}

	actualMetrics := make(map[string]HostResourceMetric, 0)

	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["compute_host"] + "-" + labels["resource"]

		actualMetrics[key] = HostResourceMetric{
			ComputeHost:      labels["compute_host"],
			Resource:         labels["resource"],
			AvailabilityZone: labels["availability_zone"],
			Enabled:          labels["enabled"],
			CPUArchitecture:  labels["cpu_architecture"],
			WorkloadType:     labels["workload_type"],
			HypervisorFamily: labels["hypervisor_family"],
			PinnedProjects:   labels["pinned_projects"],
			Value:            m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]HostResourceMetric{
		"vmware-host-cpu": {
			ComputeHost:      "vmware-host",
			Resource:         "cpu",
			AvailabilityZone: "az1",
			Enabled:          "true",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			HypervisorFamily: "vmware",
			PinnedProjects:   "project-123,project-456",
			Value:            100,
		},
		"vmware-host-ram": {
			ComputeHost:      "vmware-host",
			Resource:         "ram",
			AvailabilityZone: "az1",
			Enabled:          "true",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			HypervisorFamily: "vmware",
			PinnedProjects:   "project-123,project-456",
			Value:            200,
		},
		"vmware-host-disk": {
			ComputeHost:      "vmware-host",
			Resource:         "disk",
			AvailabilityZone: "az1",
			Enabled:          "true",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			HypervisorFamily: "vmware",
			PinnedProjects:   "project-123,project-456",
			Value:            300,
		},
		"kvm-host-cpu": {
			ComputeHost:      "kvm-host",
			Resource:         "cpu",
			AvailabilityZone: "az2",
			Enabled:          "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			HypervisorFamily: "kvm",
			PinnedProjects:   "",
			Value:            100,
		},
		"kvm-host-ram": {
			ComputeHost:      "kvm-host",
			Resource:         "ram",
			AvailabilityZone: "az2",
			Enabled:          "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			HypervisorFamily: "kvm",
			PinnedProjects:   "",
			Value:            100,
		},
		"kvm-host-disk": {
			ComputeHost:      "kvm-host",
			Resource:         "disk",
			AvailabilityZone: "az2",
			Enabled:          "false",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "hana",
			HypervisorFamily: "kvm",
			PinnedProjects:   "",
			Value:            100,
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

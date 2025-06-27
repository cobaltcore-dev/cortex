// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
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

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisor table
	hypervisors := []any{
		&nova.Hypervisor{ID: "1", ServiceHost: "host1"},
		&nova.Hypervisor{ID: "2", ServiceHost: "host2"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the host space table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50, VCPUsUtilizedPct: 50, DiskUtilizedPct: 50, TotalMemoryAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80, VCPUsUtilizedPct: 75, DiskUtilizedPct: 80, TotalMemoryAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the aggregates table
	availabilityZone1 := "zone1"
	availabilityZone2 := "zone2"
	aggregates := []any{
		&nova.Aggregate{Name: "zone1", AvailabilityZone: &availabilityZone1, ComputeHost: "host1"},
		&nova.Aggregate{Name: "zone2", AvailabilityZone: &availabilityZone2, ComputeHost: "host2"},
		&nova.Aggregate{Name: "something-else", AvailabilityZone: &availabilityZone2, ComputeHost: "host2"},
	}
	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostTotalCapacityKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0

	// Used to track the number of metrics related to each host
	metricsHost := make(map[string][]string)

	metricsHost["host1"] = make([]string, 0)
	metricsHost["host2"] = make([]string, 0)

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

		availabilityZone := labels["availability_zone"]
		computeHostName := labels["compute_host_name"]

		switch computeHostName {
		case "host1":
			if availabilityZone != "zone1" {
				t.Errorf("expected availability zone for host1 to be zone1, got %s", availabilityZone)
			}
		case "host2":
			if availabilityZone != "zone2" {
				t.Errorf("expected availability zone for host2 to be zone2, got %s", availabilityZone)
			}
		default:
			t.Errorf("unexpected compute host name: %s", computeHostName)
		}
		metricsHost[computeHostName] = append(metricsHost[computeHostName], labels["resource"])
	}

	for host, resources := range metricsHost {
		// Since we store cpu, disk and memory utilization for each host we expect 3 metrics per host
		if len(resources) != 3 {
			t.Errorf("expected 3 metrics for host %s, got %d", host, len(resources))
		}
		joinedResources := strings.Join(resources, ", ")

		if !strings.Contains(joinedResources, "memory") ||
			!strings.Contains(joinedResources, "disk") ||
			!strings.Contains(joinedResources, "cpu") {
			t.Errorf("expected resources for host %s to include memory, disk, and cpu, got %s", host, joinedResources)
		}
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}

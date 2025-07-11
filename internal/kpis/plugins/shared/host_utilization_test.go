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

func TestHostUtilizationKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostUtilizationKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostUtilizationKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(shared.HostCapabilities{}),
		testDB.AddTable(shared.HostDomainProject{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisor table
	hypervisors := []any{
		&nova.Hypervisor{ID: "1", ServiceHost: "host1", CPUInfo: `{"model": "Test CPU Model"}`, RunningVMs: 10},
		&nova.Hypervisor{ID: "2", ServiceHost: "host2", CPUInfo: `{}`, RunningVMs: 5},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the host capabilities table
	hostCapabilities := []any{
		&shared.HostCapabilities{ComputeHost: "host1", Traits: "MY_IMPORTANT_TRAIT,MY_OTHER_TRAIT"},
		&shared.HostCapabilities{ComputeHost: "host2", Traits: "MY_OTHER_TRAIT"},
	}
	if err := testDB.Insert(hostCapabilities...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the host utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{
			ComputeHost:              "host1",
			RAMUtilizedPct:           50,
			VCPUsUtilizedPct:         50,
			DiskUtilizedPct:          50,
			TotalMemoryAllocatableMB: 1000,
			TotalVCPUsAllocatable:    100,
			TotalDiskAllocatableGB:   100,
		},
		&shared.HostUtilization{
			ComputeHost:              "host2",
			RAMUtilizedPct:           80,
			VCPUsUtilizedPct:         75,
			DiskUtilizedPct:          80,
			TotalMemoryAllocatableMB: 1000,
			TotalVCPUsAllocatable:    100,
			TotalDiskAllocatableGB:   100,
		},
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostDomainProject := []any{
		&shared.HostDomainProject{
			ComputeHost:  "host1",
			ProjectNames: "project1,project2",
			ProjectIDs:   "p1,p2",
			DomainNames:  "domain1,domain2",
			DomainIDs:    "d1,d2",
		},
		&shared.HostDomainProject{
			ComputeHost:  "host2",
			ProjectNames: "project2",
			ProjectIDs:   "p2",
		},
	}
	if err := testDB.Insert(hostDomainProject...); err != nil {
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

	kpi := &HostUtilizationKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0

	// Used to track the number of metrics related to each host
	// (ignoring the histogram metric)
	metricsUtilizationLabels := make(map[string][]string)
	metricsUtilizationLabels["host1"] = make([]string, 0)
	metricsUtilizationLabels["host2"] = make([]string, 0)

	metricsTotalCapacityLabels := make(map[string][]string)
	metricsTotalCapacityLabels["host1"] = make([]string, 0)
	metricsTotalCapacityLabels["host2"] = make([]string, 0)

	for metric := range ch {
		metricsCount++

		desc := metric.Desc()
		metricName := desc.String()

		// We check if the join of the tables used for the KPI works correctly
		// That is why we skip metrics that are not related to the host utilization KPI (e.g. the histogram metric)
		if strings.Contains(metricName, "cortex_host_utilization_per_host_pct") {
			var m prometheusgo.Metric
			if err := metric.Write(&m); err != nil {
				t.Fatalf("failed to write metric: %v", err)
			}

			labels := make(map[string]string)
			for _, label := range m.Label {
				labels[label.GetName()] = label.GetValue()
			}

			cpuModel := labels["cpu_model"]
			availabilityZone := labels["availability_zone"]
			computeHostName := labels["compute_host_name"]
			runningVMs := labels["running_vms"]
			traits := labels["traits"]
			domains := labels["domains"]
			projects := labels["projects"]

			switch computeHostName {
			case "host1":
				if cpuModel != "Test CPU Model" || availabilityZone != "zone1" || runningVMs != "10" || traits != "MY_IMPORTANT_TRAIT,MY_OTHER_TRAIT" || domains != "domain1,domain2" || projects != "project1,project2" {
					t.Errorf("expected host1 to have CPU model 'Test CPU Model', availability zone 'zone1', running vms '10', traits 'MY_IMPORTANT_TRAIT,MY_OTHER_TRAIT', domains 'domain1,domain2', projects 'project1,project2', got CPU model '%s', availability zone '%s', running vms '%s', traits '%s', domains '%s', projects '%s'", cpuModel, availabilityZone, runningVMs, traits, domains, projects)
				}
			case "host2":
				if cpuModel != "" || availabilityZone != "zone2" || runningVMs != "5" || traits != "MY_OTHER_TRAIT" || domains != "" || projects != "project2" {
					t.Errorf("expected host2 to have no CPU model, availability zone 'zone2', running vms '5', traits 'MY_OTHER_TRAIT', domains '', projects 'project2', got CPU model '%s', availability zone '%s', running vms '%s', traits '%s', domains '%s', projects '%s'", cpuModel, availabilityZone, runningVMs, traits, domains, projects)
				}
			default:
				t.Errorf("unexpected compute host name: %s", computeHostName)
			}
			metricsUtilizationLabels[computeHostName] = append(metricsUtilizationLabels[computeHostName], labels["resource"])
		}

		if strings.Contains(metricName, "cortex_total_capacity_per_host") {
			var m prometheusgo.Metric
			if err := metric.Write(&m); err != nil {
				t.Fatalf("failed to write metric: %v", err)
			}

			labels := make(map[string]string)
			for _, label := range m.Label {
				labels[label.GetName()] = label.GetValue()
			}

			cpuModel := labels["cpu_model"]
			availabilityZone := labels["availability_zone"]
			computeHostName := labels["compute_host_name"]
			traits := labels["traits"]
			domains := labels["domains"]
			projects := labels["projects"]

			switch computeHostName {
			case "host1":
				if cpuModel != "Test CPU Model" || availabilityZone != "zone1" || traits != "MY_IMPORTANT_TRAIT,MY_OTHER_TRAIT" || domains != "domain1,domain2" || projects != "project1,project2" {
					t.Errorf("expected host1 to have CPU model 'Test CPU Model', availability zone 'zone1', traits 'MY_IMPORTANT_TRAIT,MY_OTHER_TRAIT', domains 'domain1,domain2', projects 'project1,project2', got CPU model '%s', availability zone '%s', traits '%s', domains '%s', projects '%s'", cpuModel, availabilityZone, traits, domains, projects)
				}
			case "host2":
				if cpuModel != "" || availabilityZone != "zone2" || traits != "MY_OTHER_TRAIT" || domains != "" || projects != "project2" {
					t.Errorf("expected host2 to have CPU model '', availability zone 'zone2', traits 'MY_OTHER_TRAIT', domains '', projects 'project2', got CPU model '%s',	 availability zone '%s', traits '%s', domains '%s', projects '%s'", cpuModel, availabilityZone, traits, domains, projects)
				}
			default:
				t.Errorf("unexpected compute host name: %s", computeHostName)
			}
			metricsTotalCapacityLabels[computeHostName] = append(metricsTotalCapacityLabels[computeHostName], labels["resource"])
		}
	}

	for host, resources := range metricsUtilizationLabels {
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

	for host, resources := range metricsTotalCapacityLabels {
		// Since we store cpu, disk and memory total capacity for each host we expect 3 metrics per host
		if len(resources) != 3 {
			t.Errorf("expected 3 total capacity metrics for host %s, got %d", host, len(resources))
		}
		joinedResources := strings.Join(resources, ", ")
		if !strings.Contains(joinedResources, "memory") ||
			!strings.Contains(joinedResources, "disk") ||
			!strings.Contains(joinedResources, "cpu") {
			t.Errorf("expected total capacity resources for host %s to include memory, disk, and cpu, got %s", host, joinedResources)
		}
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}

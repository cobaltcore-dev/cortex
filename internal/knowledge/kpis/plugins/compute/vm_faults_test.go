// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestVMFaultsKPI_GetName(t *testing.T) {
	kpi := VMFaultsKPI{}
	if kpi.GetName() != "vm_faults_kpi" {
		t.Errorf("expected 'vm_faults_kpi', got %q", kpi.GetName())
	}
}

func TestVMFaultsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	kpi := &VMFaultsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if kpi.vmFaultsDesc == nil {
		t.Error("vmFaultsDesc should be initialized")
	}
}

func TestVMFaultsKPI_Describe(t *testing.T) {
	kpi := &VMFaultsKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan *prometheus.Desc, 1)
	kpi.Describe(ch)
	close(ch)

	desc := <-ch
	if desc == nil {
		t.Error("expected descriptor to be sent to channel")
	}
}

func TestVMFaultsKPI_Collect_NoDB(t *testing.T) {
	kpi := &VMFaultsKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Collect should not panic when no database is provided
	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 metrics when no DB, got %d", count)
	}
}

func TestVMFaultsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock flavors with different hypervisor types
	flavors := []any{
		&nova.Flavor{
			ID:         "flavor-qemu",
			Name:       "qemu-small",
			VCPUs:      2,
			RAM:        4096,
			ExtraSpecs: `{"capabilities:hypervisor_type":"QEMU"}`,
		},
		&nova.Flavor{
			ID:         "flavor-vmware",
			Name:       "vmware-medium",
			VCPUs:      4,
			RAM:        8192,
			ExtraSpecs: `{"capabilities:hypervisor_type":"VMware vCenter Server"}`,
		},
		&nova.Flavor{
			ID:         "flavor-unspecified",
			Name:       "generic-large",
			VCPUs:      8,
			RAM:        16384,
			ExtraSpecs: `{}`,
		},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock servers
	servers := []any{
		// Normal server without fault
		&nova.Server{
			ID:                    "server-1",
			Name:                  "normal-vm",
			Status:                "ACTIVE",
			FlavorName:            "qemu-small",
			OSEXTAvailabilityZone: "az1",
		},
		// Server with fault code and message
		&nova.Server{
			ID:                    "server-2",
			Name:                  "faulty-vm",
			Status:                "ERROR",
			FlavorName:            "qemu-small",
			OSEXTAvailabilityZone: "az1",
			FaultCode:             testlib.Ptr(uint(500)),
			FaultMessage:          testlib.Ptr("Internal error"),
		},
		// Another faulty server in different AZ
		&nova.Server{
			ID:                    "server-3",
			Name:                  "another-faulty",
			Status:                "ERROR",
			FlavorName:            "vmware-medium",
			OSEXTAvailabilityZone: "az2",
			FaultCode:             testlib.Ptr(uint(400)),
			FaultMessage:          testlib.Ptr("Bad request"),
		},
		// Server with only fault message (no code)
		&nova.Server{
			ID:                    "server-4",
			Name:                  "partial-fault",
			Status:                "BUILD",
			FlavorName:            "generic-large",
			OSEXTAvailabilityZone: "az1",
			FaultMessage:          testlib.Ptr("Some warning"),
		},
		// Server with flavor that doesn't exist (should be skipped)
		&nova.Server{
			ID:                    "server-5",
			Name:                  "orphan-vm",
			Status:                "ACTIVE",
			FlavorName:            "nonexistent-flavor",
			OSEXTAvailabilityZone: "az1",
		},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMFaultsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type vmFaultsMetric struct {
		az           string
		hvtype       string
		state        string
		faultCode    string
		faultMessage string
		faultyVM     string
		value        float64
	}

	metrics := make(map[string]vmFaultsMetric)
	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		key := labels["az"] + "|" + labels["hvtype"] + "|" + labels["state"] + "|" +
			labels["fault-code"] + "|" + labels["faulty-vm"]

		metrics[key] = vmFaultsMetric{
			az:           labels["az"],
			hvtype:       labels["hvtype"],
			state:        labels["state"],
			faultCode:    labels["fault-code"],
			faultMessage: labels["fault-message"],
			faultyVM:     labels["faulty-vm"],
			value:        m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]vmFaultsMetric{
		// Normal VM without fault
		"az1|QEMU|ACTIVE|0|no": {
			az:           "az1",
			hvtype:       "QEMU",
			state:        "ACTIVE",
			faultCode:    "0",
			faultMessage: "n/a",
			faultyVM:     "no",
			value:        1,
		},
		// Faulty VM with code 500
		"az1|QEMU|ERROR|500|server-2": {
			az:           "az1",
			hvtype:       "QEMU",
			state:        "ERROR",
			faultCode:    "500",
			faultMessage: "Internal error",
			faultyVM:     "server-2",
			value:        1,
		},
		// Faulty VM with code 400 in az2
		"az2|VMware vCenter Server|ERROR|400|server-3": {
			az:           "az2",
			hvtype:       "VMware vCenter Server",
			state:        "ERROR",
			faultCode:    "400",
			faultMessage: "Bad request",
			faultyVM:     "server-3",
			value:        1,
		},
		// Server with only fault message (code=0 but has message)
		"az1|Unspecified|BUILD|0|server-4": {
			az:           "az1",
			hvtype:       "Unspecified",
			state:        "BUILD",
			faultCode:    "0",
			faultMessage: "Some warning",
			faultyVM:     "server-4",
			value:        1,
		},
	}

	if len(expectedMetrics) != len(metrics) {
		t.Errorf("expected %d metrics, got %d", len(expectedMetrics), len(metrics))
		t.Logf("actual metrics: %+v", metrics)
	}

	for key, expected := range expectedMetrics {
		actual, ok := metrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}

		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

func TestVMFaultsKPI_Collect_InvalidExtraSpecs(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert flavor with invalid extra specs JSON
	flavors := []any{
		&nova.Flavor{
			ID:         "flavor-bad",
			Name:       "bad-flavor",
			VCPUs:      2,
			RAM:        4096,
			ExtraSpecs: `invalid-json`,
		},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	servers := []any{
		&nova.Server{
			ID:                    "server-bad",
			Name:                  "bad-vm",
			Status:                "ACTIVE",
			FlavorName:            "bad-flavor",
			OSEXTAvailabilityZone: "az1",
		},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMFaultsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should not panic, but should skip the server with invalid flavor
	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}
	// Should have 0 metrics since the server's flavor has invalid extra specs
	if count != 0 {
		t.Errorf("expected 0 metrics, got %d", count)
	}
}

func TestVMFaultsKPI_Collect_MultipleSameLabels(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	flavors := []any{
		&nova.Flavor{
			ID:         "flavor-1",
			Name:       "small",
			VCPUs:      2,
			RAM:        4096,
			ExtraSpecs: `{"capabilities:hypervisor_type":"QEMU"}`,
		},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert multiple servers that should aggregate to same metric
	servers := []any{
		&nova.Server{
			ID:                    "server-1",
			Name:                  "vm-1",
			Status:                "ACTIVE",
			FlavorName:            "small",
			OSEXTAvailabilityZone: "az1",
		},
		&nova.Server{
			ID:                    "server-2",
			Name:                  "vm-2",
			Status:                "ACTIVE",
			FlavorName:            "small",
			OSEXTAvailabilityZone: "az1",
		},
		&nova.Server{
			ID:                    "server-3",
			Name:                  "vm-3",
			Status:                "ACTIVE",
			FlavorName:            "small",
			OSEXTAvailabilityZone: "az1",
		},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMFaultsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	var value float64
	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		value = m.GetGauge().GetValue()
	}

	// All 3 VMs should be counted together since they have the same labels
	if value != 3 {
		t.Errorf("expected metric value 3, got %f", value)
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestVMStateKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMStateKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMStateKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mockData := []any{
		// Servers in different AZs, states, and with different flavors
		&nova.Server{
			ID:                    "server-1",
			FlavorName:            "m1.small",
			OSEXTAvailabilityZone: "az1",
			Status:                "ACTIVE",
		},
		&nova.Server{
			ID:                    "server-2",
			FlavorName:            "m1.small",
			OSEXTAvailabilityZone: "az1",
			Status:                "ACTIVE",
		},
		&nova.Server{
			ID:                    "server-3",
			FlavorName:            "m1.small",
			OSEXTAvailabilityZone: "az1",
			Status:                "STOPPED",
		},
		&nova.Server{
			ID:                    "server-4",
			FlavorName:            "m1.vmware",
			OSEXTAvailabilityZone: "az2",
			Status:                "ACTIVE",
		},
		&nova.Server{
			ID:                    "server-5",
			FlavorName:            "m1.generic",
			OSEXTAvailabilityZone: "az1",
			Status:                "PAUSED",
		},
		// Flavors with different hypervisor types
		&nova.Flavor{
			ID:         "flavor-1",
			Name:       "m1.small",
			ExtraSpecs: `{"capabilities:hypervisor_type": "QEMU"}`,
		},
		&nova.Flavor{
			ID:         "flavor-2",
			Name:       "m1.vmware",
			ExtraSpecs: `{"capabilities:hypervisor_type": "VMware vCenter Server"}`,
		},
		&nova.Flavor{
			ID:         "flavor-3",
			Name:       "m1.generic",
			ExtraSpecs: `{}`,
		},
	}

	if err := testDB.Insert(mockData...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMStateKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type vmStateMetric struct {
		az     string
		hvtype string
		state  string
		count  float64
	}

	metrics := make(map[string]vmStateMetric)
	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}
		key := labels["az"] + "|" + labels["hvtype"] + "|" + labels["state"]
		metrics[key] = vmStateMetric{
			az:     labels["az"],
			hvtype: labels["hvtype"],
			state:  labels["state"],
			count:  m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]vmStateMetric{
		"az1|QEMU|ACTIVE": {
			az:     "az1",
			hvtype: "QEMU",
			state:  "ACTIVE",
			count:  2,
		},
		"az1|QEMU|STOPPED": {
			az:     "az1",
			hvtype: "QEMU",
			state:  "STOPPED",
			count:  1,
		},
		"az2|VMware vCenter Server|ACTIVE": {
			az:     "az2",
			hvtype: "VMware vCenter Server",
			state:  "ACTIVE",
			count:  1,
		},
		"az1|Unspecified|PAUSED": {
			az:     "az1",
			hvtype: "Unspecified",
			state:  "PAUSED",
			count:  1,
		},
	}

	if len(expectedMetrics) != len(metrics) {
		t.Errorf("expected %d metrics, got %d", len(expectedMetrics), len(metrics))
	}

	for key, expected := range expectedMetrics {
		actual, ok := metrics[key]
		if !ok {
			t.Errorf("expected metric %q not found", key)
			continue
		}
		if expected != actual {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

func TestVMStateKPI_Collect_MissingFlavor(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mockData := []any{
		&nova.Server{
			ID:                    "server-1",
			FlavorName:            "m1.existing",
			OSEXTAvailabilityZone: "az1",
			Status:                "ACTIVE",
		},
		&nova.Server{
			ID:                    "server-2",
			FlavorName:            "m1.missing",
			OSEXTAvailabilityZone: "az1",
			Status:                "ACTIVE",
		},
		&nova.Flavor{
			ID:         "flavor-1",
			Name:       "m1.existing",
			ExtraSpecs: `{}`,
		},
	}

	if err := testDB.Insert(mockData...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMStateKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	var count int
	for range ch {
		count++
	}
	if count != 1 {
		t.Errorf("expected 1 metric (missing flavor should be skipped), got %d", count)
	}
}

func TestVMStateKPI_Collect_NoDB(t *testing.T) {
	kpi := &VMStateKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch) // Should not panic
	close(ch)

	var count int
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 metrics when no DB, got %d", count)
	}
}

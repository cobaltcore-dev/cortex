// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

func TestFlavorRunningVMsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &FlavorRunningVMsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestFlavorRunningVMsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mockData := []any{
		&nova.Server{
			ID:                    "id-1",
			FlavorName:            "small",
			OSEXTAvailabilityZone: "zone1",
		},
		&nova.Server{
			ID:                    "id-2",
			FlavorName:            "medium",
			OSEXTAvailabilityZone: "zone1",
		},
		&nova.Server{
			ID:                    "id-3",
			FlavorName:            "medium",
			OSEXTAvailabilityZone: "zone2",
		},
		&nova.Server{
			ID:                    "id-4",
			FlavorName:            "medium",
			OSEXTAvailabilityZone: "zone2",
		},
	}

	if err := testDB.Insert(mockData...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &FlavorRunningVMsKPI{}
	if err := kpi.Init(&testDB, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)
	close(ch)

	type FlavorRunningVMsMetric struct {
		FlavorName       string
		AvailabilityZone string
		RunningVMs       float64
	}

	metrics := make(map[string]FlavorRunningVMsMetric, 0)

	for metric := range ch {
		var m prometheusgo.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}

		labels := make(map[string]string)
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}

		flavor := labels["flavor_name"]
		availabilityZone := labels["availability_zone"]

		key := flavor + "|" + availabilityZone

		metrics[key] = FlavorRunningVMsMetric{
			FlavorName:       flavor,
			AvailabilityZone: availabilityZone,
			RunningVMs:       m.GetGauge().GetValue(),
		}
	}

	expectedMetrics := map[string]FlavorRunningVMsMetric{
		"small|zone1": {
			FlavorName:       "small",
			AvailabilityZone: "zone1",
			RunningVMs:       1,
		},
		"medium|zone1": {
			FlavorName:       "medium",
			AvailabilityZone: "zone1",
			RunningVMs:       1,
		},
		"medium|zone2": {
			FlavorName:       "medium",
			AvailabilityZone: "zone2",
			RunningVMs:       2,
		},
	}

	for key, expected := range expectedMetrics {
		actual, ok := metrics[key]
		if !ok {
			t.Logf("%v", metrics)
			t.Errorf("expected metric %q not found", key)
			continue
		}

		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
		}
	}
}

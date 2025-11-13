// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/limes"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMCommitmentsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMCommitmentsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify that all metric descriptors are initialized
	if kpi.vmCommitmentsTotalDesc == nil {
		t.Error("vmCommitmentsTotalDesc should be initialized")
	}
	if kpi.vmCommitmentsSumDesc == nil {
		t.Error("vmCommitmentsSumDesc should be initialized")
	}
	if kpi.committedCoresDesc == nil {
		t.Error("committedCoresDesc should be initialized")
	}
	if kpi.committedMemoryDesc == nil {
		t.Error("committedMemoryDesc should be initialized")
	}
}

func TestVMCommitmentsKPI_GetName(t *testing.T) {
	kpi := &VMCommitmentsKPI{}
	expected := "vm_commitments_kpi"
	if got := kpi.GetName(); got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestConvertLimesMemory(t *testing.T) {
	tests := []struct {
		unit     string
		expected float64
		hasError bool
	}{
		{"B", 1, false},
		{"", 1, false},
		{"KiB", 1024, false},
		{"MiB", 1024 * 1024, false},
		{"GiB", 1024 * 1024 * 1024, false},
		{"TiB", 1024 * 1024 * 1024 * 1024, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.unit, func(t *testing.T) {
			result, err := convertLimesMemory(tt.unit)
			if tt.hasError {
				if err == nil {
					t.Errorf("expected error for unit %s, got nil", tt.unit)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error for unit %s, got %v", tt.unit, err)
				}
				if result != tt.expected {
					t.Errorf("expected %f for unit %s, got %f", tt.expected, tt.unit, result)
				}
			}
		})
	}
}

func TestVMCommitmentsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(limes.Commitment{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock flavors
	flavors := []any{
		&nova.Flavor{ID: "1", Name: "small", VCPUs: 2, RAM: 4096},  // 4GB RAM
		&nova.Flavor{ID: "2", Name: "medium", VCPUs: 4, RAM: 8192}, // 8GB RAM
		&nova.Flavor{ID: "3", Name: "large", VCPUs: 8, RAM: 16384}, // 16GB RAM
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock commitments
	commitments := []any{
		// Instance commitments
		&limes.Commitment{
			ID:               1,
			ServiceType:      "compute",
			ResourceName:     "instances_small",
			AvailabilityZone: "zone1",
			Amount:           2,
			Status:           "confirmed",
		},
		&limes.Commitment{
			ID:               2,
			ServiceType:      "compute",
			ResourceName:     "instances_medium",
			AvailabilityZone: "zone1",
			Amount:           1,
			Status:           "pending",
		},
		// Core commitments
		&limes.Commitment{
			ID:               3,
			ServiceType:      "compute",
			ResourceName:     "cores",
			AvailabilityZone: "zone2",
			Amount:           10,
			Status:           "confirmed",
		},
		// RAM commitments
		&limes.Commitment{
			ID:               4,
			ServiceType:      "compute",
			ResourceName:     "ram",
			AvailabilityZone: "zone2",
			Amount:           8,
			Unit:             "GiB",
			Status:           "confirmed",
		},
		&limes.Commitment{
			ID:               5,
			ServiceType:      "compute",
			ResourceName:     "ram",
			AvailabilityZone: "zone1",
			Amount:           2048,
			Unit:             "MiB",
			Status:           "pending",
		},
		// Non-compute service (should be ignored)
		&limes.Commitment{
			ID:               6,
			ServiceType:      "storage",
			ResourceName:     "volumes",
			AvailabilityZone: "zone1",
			Amount:           5,
			Status:           "confirmed",
		},
		// Unknown flavor (should be ignored with warning)
		&limes.Commitment{
			ID:               7,
			ServiceType:      "compute",
			ResourceName:     "instances_unknown",
			AvailabilityZone: "zone1",
			Amount:           1,
			Status:           "confirmed",
		},
		// Unknown resource name (should be ignored with warning)
		&limes.Commitment{
			ID:               8,
			ServiceType:      "compute",
			ResourceName:     "unknown_resource",
			AvailabilityZone: "zone1",
			Amount:           1,
			Status:           "confirmed",
		},
	}
	if err := testDB.Insert(commitments...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMCommitmentsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 100)
	kpi.Collect(ch)

	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}

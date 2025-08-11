// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostDetailsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostAZExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_az_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostAZ{}) {
		t.Error("expected table to be created")
	}
}

func TestHostAZExtractor_Extract(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(nova.Aggregate{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	computeHost1 := "host1"
	computeHost2 := "host2"
	computeHost3 := "host3"
	computeHost4 := "host4"

	// Insert mock data into the hypervisors and traits tables
	hypervisors := []any{
		&nova.Hypervisor{ID: "uuid1", ServiceHost: computeHost1},
		&nova.Hypervisor{ID: "uuid2", ServiceHost: computeHost2},
		&nova.Hypervisor{ID: "uuid3", ServiceHost: computeHost3},
		&nova.Hypervisor{ID: "uuid4", ServiceHost: computeHost4},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	availabilityZone1 := "az1"
	availabilityZone2 := "az2"

	aggregates := []any{
		// Test to find the first aggregate for computeHost1 with availability_zone != null
		&nova.Aggregate{UUID: "agg1", Name: "something_else", AvailabilityZone: nil, ComputeHost: &computeHost1, Metadata: "{}"},
		&nova.Aggregate{UUID: "agg2", Name: availabilityZone1, AvailabilityZone: &availabilityZone1, ComputeHost: &computeHost1, Metadata: "{}"},
		// Test to check that we get null when there is an aggregate for computeHost2 but without availability_zone
		&nova.Aggregate{UUID: "agg3", Name: "something_else_again", AvailabilityZone: nil, ComputeHost: &computeHost2, Metadata: "{}"},
		// No aggregate for computeHost3
		// Should find an availability zone for computeHost4
		&nova.Aggregate{UUID: "agg4", Name: availabilityZone2, AvailabilityZone: &availabilityZone2, ComputeHost: &computeHost4, Metadata: "{}"},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostAZExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_az_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedHostAZs := map[string]HostAZ{}
	expectedHostAZs[computeHost1] = HostAZ{
		ComputeHost:      computeHost1,
		AvailabilityZone: &availabilityZone1,
	}
	// Aggregate without availability_zone provided for host
	expectedHostAZs[computeHost2] = HostAZ{
		ComputeHost:      computeHost2,
		AvailabilityZone: nil,
	}
	// No aggregate provided for host
	expectedHostAZs[computeHost3] = HostAZ{
		ComputeHost:      computeHost3,
		AvailabilityZone: nil,
	}
	expectedHostAZs[computeHost4] = HostAZ{
		ComputeHost:      computeHost4,
		AvailabilityZone: &availabilityZone2,
	}

	var hostAZs []HostAZ
	_, err := testDB.Select(&hostAZs, "SELECT * FROM "+HostAZ{}.TableName())
	if err != nil {
		t.Fatalf("expected no error from Extract, got %v", err)
	}

	for _, hostAZ := range hostAZs {
		expected, ok := expectedHostAZs[hostAZ.ComputeHost]
		if !ok {
			t.Errorf("unexpected host AZ: %+v", hostAZ)
			continue
		}
		if (hostAZ.AvailabilityZone == nil) != (expected.AvailabilityZone == nil) ||
			(hostAZ.AvailabilityZone != nil && expected.AvailabilityZone != nil && *hostAZ.AvailabilityZone != *expected.AvailabilityZone) {
			t.Errorf("expected host AZ for %s to be %+v, got %+v", hostAZ.ComputeHost, expected, hostAZ)
		}
	}
}

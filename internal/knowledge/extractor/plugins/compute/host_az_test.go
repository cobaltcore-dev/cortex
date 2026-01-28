// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"os"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

func TestHostAZExtractor_Init(t *testing.T) {
	extractor := &HostAZExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostAZExtractor_Extract(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(nova.Aggregate{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and traits tables
	hypervisors := []any{
		&nova.Hypervisor{ID: "uuid1", ServiceHost: "host1"},
		&nova.Hypervisor{ID: "uuid2", ServiceHost: "host2"},
		&nova.Hypervisor{ID: "uuid3", ServiceHost: "host3"},
		&nova.Hypervisor{ID: "uuid4", ServiceHost: "host4"},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	aggregates := []any{
		// Test to find the first aggregate for computeHost1 with availability_zone != null
		&nova.Aggregate{UUID: "agg1", Name: "something_else", AvailabilityZone: nil, ComputeHost: testlib.Ptr("host1"), Metadata: "{}"},
		&nova.Aggregate{UUID: "agg2", Name: "az1", AvailabilityZone: testlib.Ptr("az1"), ComputeHost: testlib.Ptr("host1"), Metadata: "{}"},
		// Test to check that we get null when there is an aggregate for computeHost2 but without availability_zone
		&nova.Aggregate{UUID: "agg3", Name: "something_else_again", AvailabilityZone: nil, ComputeHost: testlib.Ptr("host2"), Metadata: "{}"},
		// No aggregate for computeHost3
		// Should find an availability zone for computeHost4
		&nova.Aggregate{UUID: "agg4", Name: "az2", AvailabilityZone: testlib.Ptr("az2"), ComputeHost: testlib.Ptr("host4"), Metadata: "{}"},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostAZExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedHostAZs := []HostAZ{
		{
			ComputeHost:      "host1",
			AvailabilityZone: testlib.Ptr("az1"),
		},
		// Aggregate without availability_zone provided for host
		{
			ComputeHost:      "host2",
			AvailabilityZone: nil,
		},
		// No aggregate provided for host
		{
			ComputeHost:      "host3",
			AvailabilityZone: nil,
		},
		{
			ComputeHost:      "host4",
			AvailabilityZone: testlib.Ptr("az2"),
		},
	}

	if len(features) != len(expectedHostAZs) {
		t.Errorf("expected %d host AZs, got %d", len(expectedHostAZs), len(features))
	}

	for idx, f := range features {
		hostAZ := f.(HostAZ)
		if !reflect.DeepEqual(hostAZ, expectedHostAZs[idx]) {
			t.Errorf("expected host AZ for %s to be %+v, got %+v", hostAZ.ComputeHost, expectedHostAZs[idx], hostAZ)
		}
	}
}

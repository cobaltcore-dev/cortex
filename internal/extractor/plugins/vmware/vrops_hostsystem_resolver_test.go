// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVROpsHostsystemResolver_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VROpsHostsystemResolver{}

	config := conf.FeatureExtractorConfig{
		Name:           "vrops_hostsystem_resolver",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the table was created
	if !testDB.TableExists(ResolvedVROpsHostsystem{}) {
		t.Error("expected table to be created")
	}
}

func TestVROpsHostsystemResolver_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.VROpsVMMetric{}),
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.DeletedServer{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsVMMetrics := []any{
		&prometheus.VROpsVMMetric{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
		&prometheus.VROpsVMMetric{HostSystem: "hostsystem2", InstanceUUID: "uuid2"},
		&prometheus.VROpsVMMetric{HostSystem: "hostsystem3", InstanceUUID: "uuid3"}, // Deleted server
	}
	if err := testDB.Insert(vropsVMMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	servers := []any{
		&nova.Server{ID: "uuid1", OSEXTSRVATTRHost: "service_host1"},
		&nova.Server{ID: "uuid2", OSEXTSRVATTRHost: "service_host2"},

		&nova.DeletedServer{ID: "uuid3", OSEXTSRVATTRHost: "service_host3", Status: "DELETED"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsHostsystemResolver{}
	config := conf.FeatureExtractorConfig{
		Name:           "vrops_hostsystem_resolver",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_resolved_hostsystem table
	var resolvedHostsystems []ResolvedVROpsHostsystem
	table := ResolvedVROpsHostsystem{}.TableName()
	_, err := testDB.Select(&resolvedHostsystems, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := map[string]string{
		"hostsystem1": "service_host1",
		"hostsystem2": "service_host2",
		"hostsystem3": "service_host3",
	}
	if len(resolvedHostsystems) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(resolvedHostsystems))
	}
	for _, r := range resolvedHostsystems {
		if expected[r.VROpsHostsystem] != r.NovaComputeHost {
			t.Errorf("expected nova_compute_host for vrops_hostsystem %s to be %s, got %s", r.VROpsHostsystem, expected[r.VROpsHostsystem], r.NovaComputeHost)
		}
	}
}

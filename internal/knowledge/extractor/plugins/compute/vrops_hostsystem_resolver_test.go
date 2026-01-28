// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

func TestVROpsHostsystemResolver_Init(t *testing.T) {
	extractor := &VROpsHostsystemResolver{}

	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVROpsHostsystemResolver_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.VROpsVMMetric{}),
		testDB.AddTable(nova.Server{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsVMMetrics := []any{
		&prometheus.VROpsVMMetric{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
		&prometheus.VROpsVMMetric{HostSystem: "hostsystem2", InstanceUUID: "uuid2"},
	}
	if err := testDB.Insert(vropsVMMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	servers := []any{
		&nova.Server{ID: "uuid1", OSEXTSRVATTRHost: "service_host1"},
		&nova.Server{ID: "uuid2", OSEXTSRVATTRHost: "service_host2"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsHostsystemResolver{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := map[string]string{
		"hostsystem1": "service_host1",
		"hostsystem2": "service_host2",
	}
	if len(features) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(features))
	}
	for _, f := range features {
		r := f.(ResolvedVROpsHostsystem)
		if expected[r.VROpsHostsystem] != r.NovaComputeHost {
			t.Errorf("expected nova_compute_host for vrops_hostsystem %s to be %s, got %s", r.VROpsHostsystem, expected[r.VROpsHostsystem], r.NovaComputeHost)
		}
	}
}

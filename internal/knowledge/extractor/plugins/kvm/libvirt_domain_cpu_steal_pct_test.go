// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestLibvirtDomainCPUStealPctExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	extractor := &LibvirtDomainCPUStealPctExtractor{}

	config := v1alpha1.KnowledgeSpec{
		Extractor: v1alpha1.KnowledgeExtractorSpec{
			Name:   "kvm_libvirt_domain_cpu_steal_pct_extractor",
			Config: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}
	if err := extractor.Init(&testDB, &testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(LibvirtDomainCPUStealPct{}) {
		t.Error("expected table to be created")
	}
}

func TestLibvirtDomainCPUStealPctExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.KVMDomainMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the kvm_libvirt_domain_metrics table
	kvmMetrics := []any{
		&prometheus.KVMDomainMetric{Domain: "instance-00000001", Name: "kvm_libvirt_domain_steal_pct", Value: 15.5},
		&prometheus.KVMDomainMetric{Domain: "instance-00000001", Name: "kvm_libvirt_domain_steal_pct", Value: 25.0},
		&prometheus.KVMDomainMetric{Domain: "instance-00000002", Name: "kvm_libvirt_domain_steal_pct", Value: 10.2},
		&prometheus.KVMDomainMetric{Domain: "instance-00000003", Name: "kvm_libvirt_domain_steal_pct", Value: 30.8},
	}

	if err := testDB.Insert(kvmMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_servers table
	servers := []any{
		&nova.Server{
			ID:                       "uuid-1",
			OSEXTSRVATTRInstanceName: "instance-00000001",
			OSEXTSRVATTRHost:         "compute-host-1",
		},
		&nova.Server{
			ID:                       "uuid-2",
			OSEXTSRVATTRInstanceName: "instance-00000002",
			OSEXTSRVATTRHost:         "compute-host-2",
		},
		&nova.Server{
			ID:                       "uuid-3",
			OSEXTSRVATTRInstanceName: "instance-00000003",
			OSEXTSRVATTRHost:         "compute-host-1",
		},
	}

	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &LibvirtDomainCPUStealPctExtractor{}

	config := v1alpha1.KnowledgeSpec{
		Extractor: v1alpha1.KnowledgeExtractorSpec{
			Name:   "kvm_libvirt_domain_cpu_steal_pct_extractor",
			Config: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}
	if err := extractor.Init(&testDB, &testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_libvirt_domain_cpu_steal_pct table
	var stealPcts []LibvirtDomainCPUStealPct
	table := LibvirtDomainCPUStealPct{}.TableName()
	_, err := testDB.Select(&stealPcts, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(stealPcts) != 3 {
		t.Errorf("expected 3 rows, got %d", len(stealPcts))
	}

	expected := map[string]struct {
		Host            string
		MaxStealTimePct float64
	}{
		"uuid-1": {Host: "compute-host-1", MaxStealTimePct: 25.0}, // Max of 15.5 and 25.0
		"uuid-2": {Host: "compute-host-2", MaxStealTimePct: 10.2}, // Single value of 10.2
		"uuid-3": {Host: "compute-host-1", MaxStealTimePct: 30.8}, // Single value of 30.8
	}

	for _, s := range stealPcts {
		if expected[s.InstanceUUID].Host != s.Host {
			t.Errorf(
				"expected host for instance_uuid %s to be %s, got %s",
				s.InstanceUUID, expected[s.InstanceUUID].Host, s.Host,
			)
		}
		if expected[s.InstanceUUID].MaxStealTimePct != s.MaxStealTimePct {
			t.Errorf(
				"expected max_steal_time_pct for instance_uuid %s to be %f, got %f",
				s.InstanceUUID, expected[s.InstanceUUID].MaxStealTimePct, s.MaxStealTimePct,
			)
		}
	}
}

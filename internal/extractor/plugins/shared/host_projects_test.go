// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostDomainProjectExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostDomainProjectExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_domain_project_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostDomainProject{}) {
		t.Error("expected table to be created")
	}
}

func TestHostDomainProjectExtractor_Extract(t *testing.T) {
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
		testDB.AddTable(identity.Project{}),
		testDB.AddTable(identity.Domain{}),
		testDB.AddTable(nova.Server{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data
	hypervisors := []any{
		&nova.Hypervisor{ID: "h1", ServiceHost: "host1"},
		&nova.Hypervisor{ID: "h2", ServiceHost: "host2"},
	}
	domains := []any{
		&identity.Domain{ID: "d1", Name: "domain1", Enabled: true},
		&identity.Domain{ID: "d2", Name: "domain2", Enabled: true},
		&identity.Domain{ID: "d3", Name: "domain3", Enabled: true},
	}
	projects := []any{
		&identity.Project{ID: "p1", Name: "project1", DomainID: "d1", Enabled: true},
		&identity.Project{ID: "p2", Name: "project2", DomainID: "d2", Enabled: true},
		&identity.Project{ID: "p3", Name: "project3", DomainID: "d3", Enabled: true},
	}
	servers := []any{
		&nova.Server{ID: "s1", Name: "server1", TenantID: "p1", OSEXTSRVATTRHost: "host1", Status: "ACTIVE"},
		&nova.Server{ID: "s2", Name: "server2", TenantID: "p2", OSEXTSRVATTRHost: "host1", Status: "ACTIVE"},
		&nova.Server{ID: "s3", Name: "server3", TenantID: "p2", OSEXTSRVATTRHost: "host2", Status: "ACTIVE"},
		&nova.Server{ID: "s4", Name: "server4", TenantID: "p3", OSEXTSRVATTRHost: "host2", Status: "DELETED"}, // Should be filtered out
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := testDB.Insert(domains...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := testDB.Insert(projects...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostDomainProjectExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_domain_project_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_domain_project table
	var results []HostDomainProject
	table := HostDomainProject{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table+" ORDER BY compute_host, project_id"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check expected values (order may vary)
	expectedEntries := []HostDomainProject{
		{
			ComputeHost: "host1",
			ProjectName: "project1",
			ProjectID:   "p1",
			DomainName:  "domain1",
			DomainID:    "d1",
		},
		{
			ComputeHost: "host1",
			ProjectName: "project2",
			ProjectID:   "p2",
			DomainName:  "domain2",
			DomainID:    "d2",
		},
		{
			ComputeHost: "host2",
			ProjectName: "project2",
			ProjectID:   "p2",
			DomainName:  "domain2",
			DomainID:    "d2",
		},
	}

	if len(results) != len(expectedEntries) {
		t.Errorf("expected %d rows, got %d", len(expectedEntries), len(results))
	}

	for idx, expected := range expectedEntries {
		result := results[idx]
		if result.ComputeHost != expected.ComputeHost {
			t.Errorf("expected compute host %q, got %q", expected.ComputeHost, result.ComputeHost)
		}
		if result.ProjectName != expected.ProjectName {
			t.Errorf("expected project name %q, got %q", expected.ProjectName, result.ProjectName)
		}
		if result.ProjectID != expected.ProjectID {
			t.Errorf("expected project id %q, got %q", expected.ProjectID, result.ProjectID)
		}
		if result.DomainName != expected.DomainName {
			t.Errorf("expected domain name %q, got %q", expected.DomainName, result.DomainName)
		}
		if result.DomainID != expected.DomainID {
			t.Errorf("expected domain id %q, got %q", expected.DomainID, result.DomainID)
		}
	}
}

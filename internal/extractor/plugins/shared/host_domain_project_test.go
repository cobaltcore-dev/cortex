// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"strings"
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

// Helper to compare comma-separated string sets (ignores order)
func compareCSVSet(string1, string2 string) bool {
	valuesString1 := make(map[string]struct{})
	valuesString2 := make(map[string]struct{})
	for v := range strings.SplitSeq(string1, ",") {
		valuesString1[v] = struct{}{}
	}
	for v := range strings.SplitSeq(string2, ",") {
		valuesString2[v] = struct{}{}
	}
	if len(valuesString1) != len(valuesString2) {
		return false
	}
	for k := range valuesString1 {
		if _, ok := valuesString2[k]; !ok {
			return false
		}
	}
	return true
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

	if _, err := testDB.Select(&results, "SELECT * FROM feature_host_domain_project"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 rows, got %d", len(results))
	}

	// Check expected values (order may vary)
	expected := map[string]HostDomainProject{
		"host1": {
			ComputeHost:  "host1",
			ProjectNames: "project1,project2",
			ProjectIDs:   "p1,p2",
			DomainNames:  "domain1,domain2",
			DomainIDs:    "d1,d2",
		},
		"host2": {
			ComputeHost:  "host2",
			ProjectNames: "project2",
			ProjectIDs:   "p2",
			DomainNames:  "domain2",
			DomainIDs:    "d2",
		},
	}
	for _, r := range results {
		exp, ok := expected[r.ComputeHost]
		if !ok {
			t.Errorf("unexpected compute host: %s", r.ComputeHost)
			continue
		}
		// Compare as sets (order in STRING_AGG may vary)
		if !compareCSVSet(r.ProjectNames, exp.ProjectNames) {
			t.Errorf("expected project names %q, got %q", exp.ProjectNames, r.ProjectNames)
		}
		if !compareCSVSet(r.ProjectIDs, exp.ProjectIDs) {
			t.Errorf("expected project ids %q, got %q", exp.ProjectIDs, r.ProjectIDs)
		}
		if !compareCSVSet(r.DomainNames, exp.DomainNames) {
			t.Errorf("expected domain names %q, got %q", exp.DomainNames, r.DomainNames)
		}
		if !compareCSVSet(r.DomainIDs, exp.DomainIDs) {
			t.Errorf("expected domain ids %q, got %q", exp.DomainIDs, r.DomainIDs)
		}
	}
}

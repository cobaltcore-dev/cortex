// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	"k8s.io/apimachinery/pkg/api/resource"
)

// expectedVM creates an expected VMDetails for comparison.
// Use ramMB=0 and vcpus=0 to indicate no resources (missing flavor).
func expectedVM(serverUUID, flavorName, projectID, currentHost string, ramMB, vcpus uint64) VMDetails {
	vm := VMDetails{
		ServerUUID:  serverUUID,
		FlavorName:  flavorName,
		ProjectID:   projectID,
		CurrentHost: currentHost,
		Resources:   make(map[string]resource.Quantity),
	}
	if ramMB > 0 {
		vm.Resources["memory"] = resource.MustParse(formatMegabytes(ramMB))
	}
	if vcpus > 0 {
		vm.Resources["vcpus"] = resource.MustParse(formatVCPUs(vcpus))
	}
	return vm
}

func TestVMDetailsExtractor_Init(t *testing.T) {
	extractor := &VMDetailsExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMDetailsExtractor_Extract_NilDB(t *testing.T) {
	extractor := &VMDetailsExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := extractor.Extract()
	if err == nil {
		t.Fatal("expected error for nil DB, got nil")
	}
	if err.Error() != "database connection is not initialized" {
		t.Errorf("expected 'database connection is not initialized', got %v", err)
	}
}

func TestVMDetailsExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		flavors  []*nova.Flavor
		servers  []*nova.Server
		expected []VMDetails
	}{
		{
			name:     "empty database returns no results",
			flavors:  []*nova.Flavor{},
			servers:  []*nova.Server{},
			expected: []VMDetails{},
		},
		{
			name: "single server with matching flavor",
			flavors: []*nova.Flavor{
				{ID: "flavor-1", Name: "m1.small", RAM: 2048, VCPUs: 1},
			},
			servers: []*nova.Server{
				{ID: "server-uuid-1", Name: "test-vm-1", TenantID: "project-A", OSEXTSRVATTRHost: "host1", FlavorName: "m1.small", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("server-uuid-1", "m1.small", "project-A", "host1", 2048, 1),
			},
		},
		{
			name: "multiple servers with different flavors",
			flavors: []*nova.Flavor{
				{ID: "flavor-1", Name: "m1.small", RAM: 2048, VCPUs: 1},
				{ID: "flavor-2", Name: "m1.large", RAM: 8192, VCPUs: 4},
			},
			servers: []*nova.Server{
				{ID: "server-uuid-1", Name: "test-vm-1", TenantID: "project-A", OSEXTSRVATTRHost: "host1", FlavorName: "m1.small", Status: "ACTIVE"},
				{ID: "server-uuid-2", Name: "test-vm-2", TenantID: "project-B", OSEXTSRVATTRHost: "host2", FlavorName: "m1.large", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("server-uuid-1", "m1.small", "project-A", "host1", 2048, 1),
				expectedVM("server-uuid-2", "m1.large", "project-B", "host2", 8192, 4),
			},
		},
		{
			name:    "server with missing flavor has no resources",
			flavors: []*nova.Flavor{},
			servers: []*nova.Server{
				{ID: "server-uuid-orphan", Name: "orphan-vm", TenantID: "project-C", OSEXTSRVATTRHost: "host3", FlavorName: "nonexistent-flavor", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("server-uuid-orphan", "nonexistent-flavor", "project-C", "host3", 0, 0),
			},
		},
		{
			name: "multiple servers sharing same flavor",
			flavors: []*nova.Flavor{
				{ID: "flavor-1", Name: "m1.medium", RAM: 4096, VCPUs: 2},
			},
			servers: []*nova.Server{
				{ID: "server-1", Name: "vm-1", TenantID: "project-X", OSEXTSRVATTRHost: "host1", FlavorName: "m1.medium", Status: "ACTIVE"},
				{ID: "server-2", Name: "vm-2", TenantID: "project-X", OSEXTSRVATTRHost: "host2", FlavorName: "m1.medium", Status: "ACTIVE"},
				{ID: "server-3", Name: "vm-3", TenantID: "project-Y", OSEXTSRVATTRHost: "host1", FlavorName: "m1.medium", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("server-1", "m1.medium", "project-X", "host1", 4096, 2),
				expectedVM("server-2", "m1.medium", "project-X", "host2", 4096, 2),
				expectedVM("server-3", "m1.medium", "project-Y", "host1", 4096, 2),
			},
		},
		{
			name: "mixed: some servers with flavors, some without",
			flavors: []*nova.Flavor{
				{ID: "flavor-1", Name: "m1.small", RAM: 2048, VCPUs: 1},
			},
			servers: []*nova.Server{
				{ID: "server-with-flavor", Name: "vm-1", TenantID: "project-A", OSEXTSRVATTRHost: "host1", FlavorName: "m1.small", Status: "ACTIVE"},
				{ID: "server-without-flavor", Name: "vm-2", TenantID: "project-B", OSEXTSRVATTRHost: "host2", FlavorName: "deleted-flavor", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("server-with-flavor", "m1.small", "project-A", "host1", 2048, 1),
				expectedVM("server-without-flavor", "deleted-flavor", "project-B", "host2", 0, 0),
			},
		},
		{
			name: "large flavor values",
			flavors: []*nova.Flavor{
				{ID: "flavor-huge", Name: "m1.xlarge", RAM: 65536, VCPUs: 32},
			},
			servers: []*nova.Server{
				{ID: "big-server", Name: "big-vm", TenantID: "project-big", OSEXTSRVATTRHost: "host-big", FlavorName: "m1.xlarge", Status: "ACTIVE"},
			},
			expected: []VMDetails{
				expectedVM("big-server", "m1.xlarge", "project-big", "host-big", 65536, 32),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup database
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer dbEnv.Close()

			// Create tables
			if err := testDB.CreateTable(
				testDB.AddTable(nova.Server{}),
				testDB.AddTable(nova.Flavor{}),
			); err != nil {
				t.Fatalf("expected no error creating tables, got %v", err)
			}

			// Insert flavors
			if len(tt.flavors) > 0 {
				flavors := make([]any, len(tt.flavors))
				for i, f := range tt.flavors {
					flavors[i] = f
				}
				if err := testDB.Insert(flavors...); err != nil {
					t.Fatalf("expected no error inserting flavors, got %v", err)
				}
			}

			// Insert servers
			if len(tt.servers) > 0 {
				servers := make([]any, len(tt.servers))
				for i, s := range tt.servers {
					servers[i] = s
				}
				if err := testDB.Insert(servers...); err != nil {
					t.Fatalf("expected no error inserting servers, got %v", err)
				}
			}

			// Run extractor
			extractor := &VMDetailsExtractor{}
			config := v1alpha1.KnowledgeSpec{}
			if err := extractor.Init(&testDB, nil, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			features, err := extractor.Extract()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Verify count
			if len(features) != len(tt.expected) {
				t.Errorf("expected %d results, got %d", len(tt.expected), len(features))
				return
			}

			// Build map of actual results for easier comparison
			actualByUUID := make(map[string]VMDetails)
			for _, f := range features {
				vm := f.(VMDetails)
				actualByUUID[vm.ServerUUID] = vm
			}

			// Compare expected vs actual
			for _, exp := range tt.expected {
				actual, ok := actualByUUID[exp.ServerUUID]
				if !ok {
					t.Errorf("expected server %s not found in results", exp.ServerUUID)
					continue
				}

				if actual.FlavorName != exp.FlavorName {
					t.Errorf("server %s: expected flavor %s, got %s", exp.ServerUUID, exp.FlavorName, actual.FlavorName)
				}
				if actual.ProjectID != exp.ProjectID {
					t.Errorf("server %s: expected project %s, got %s", exp.ServerUUID, exp.ProjectID, actual.ProjectID)
				}
				if actual.CurrentHost != exp.CurrentHost {
					t.Errorf("server %s: expected host %s, got %s", exp.ServerUUID, exp.CurrentHost, actual.CurrentHost)
				}

				// Compare resources
				if len(actual.Resources) != len(exp.Resources) {
					t.Errorf("server %s: expected %d resources, got %d", exp.ServerUUID, len(exp.Resources), len(actual.Resources))
					continue
				}

				for key, expVal := range exp.Resources {
					actVal, ok := actual.Resources[key]
					if !ok {
						t.Errorf("server %s: expected resource %s not found", exp.ServerUUID, key)
						continue
					}
					if !actVal.Equal(expVal) {
						t.Errorf("server %s: resource %s: expected %v, got %v", exp.ServerUUID, key, expVal, actVal)
					}
				}
			}
		})
	}
}

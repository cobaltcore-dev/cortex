// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestProjectResourceUtilizationExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &ProjectResourceUtilizationExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "project_resource_utilization_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(ProjectResourceUtilization{}) {
		t.Error("expected table to be created")
	}
}

func TestProjectResourceUtilizationExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		mockData []any
		expected []ProjectResourceUtilization
	}{
		{
			name:     "should return empty list when no projects exist",
			mockData: []any{},
			expected: []ProjectResourceUtilization{},
		},
		{
			name: "should return empty list when projects have no servers",
			mockData: []any{
				&identity.Project{ID: "project-123"},
			},
			expected: []ProjectResourceUtilization{},
		},
		{
			name: "should have values of flavor when only one server exists",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: testlib.Ptr("az1")},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
		{
			name: "should ignore deleted servers",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},
				&nova.Server{ID: "server-2", TenantID: "project-123", FlavorName: "flavor-small", Status: "DELETED", OSEXTSRVATTRHost: "host1"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: testlib.Ptr("az1")},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
		{
			name: "should add up values of flavor when multiple servers exists",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},
				&identity.Project{ID: "project-456"},
				&identity.Project{ID: "project-789"}, // No servers for this project

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},
				&nova.Server{ID: "server-2", TenantID: "project-456", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},
				&nova.Server{ID: "server-3", TenantID: "project-456", FlavorName: "flavor-big", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
				&nova.Flavor{ID: "2", Name: "flavor-big", VCPUs: 4, RAM: 8192, Disk: 40},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: testlib.Ptr("az1")},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
				{ProjectID: "project-456", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 2, TotalVCPUsUsed: 4 + 2, TotalRAMUsedMB: 4096 + 8192, TotalDiskUsedGB: 20 + 40},
			},
		},
		{
			name: "should return entry when flavor cannot be resolved with unresolved count",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-nonexistent", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: testlib.Ptr("az1")},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 1, UnresolvedServerFlavors: 1, TotalVCPUsUsed: 0, TotalRAMUsedMB: 0, TotalDiskUsedGB: 0},
			},
		},
		{
			name: "should differentiate availability zones",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},
				&nova.Server{ID: "server-2", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host2"},
				&nova.Server{ID: "server-3", TenantID: "project-123", FlavorName: "flavor-big", Status: "ACTIVE", OSEXTSRVATTRHost: "host3"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
				&nova.Flavor{ID: "2", Name: "flavor-big", VCPUs: 4, RAM: 8192, Disk: 40},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: testlib.Ptr("az1")},
				&HostAZ{ComputeHost: "host2", AvailabilityZone: testlib.Ptr("az2")},
				&HostAZ{ComputeHost: "host3", AvailabilityZone: testlib.Ptr("az2")},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az1"), TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
				{ProjectID: "project-123", AvailabilityZone: testlib.Ptr("az2"), TotalServers: 2, TotalVCPUsUsed: 4 + 2, TotalRAMUsedMB: 4096 + 8192, TotalDiskUsedGB: 20 + 40},
			},
		},
		{
			name: "should return nil as availability zone if the host is not mapped to a availability zone",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: nil, TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
		{
			name: "should return nil as availability zone if the host has no availability zone",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE", OSEXTSRVATTRHost: "host1"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},

				// Availability zones
				&HostAZ{ComputeHost: "host1", AvailabilityZone: nil},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", AvailabilityZone: nil, TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer testDB.Close()
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(identity.Project{}),
				testDB.AddTable(nova.Server{}),
				testDB.AddTable(nova.Flavor{}),
				testDB.AddTable(HostAZ{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			extractor := &ProjectResourceUtilizationExtractor{}
			config := conf.FeatureExtractorConfig{
				Name:           "project_resource_utilization_extractor",
				Options:        conf.NewRawOpts("{}"),
				RecencySeconds: nil, // No recency for this test
			}
			if err := extractor.Init(testDB, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if _, err := extractor.Extract(); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			var projectResourceUtilizations []ProjectResourceUtilization
			_, err := testDB.Select(&projectResourceUtilizations, "SELECT * FROM "+ProjectResourceUtilization{}.TableName()+" ORDER BY project_id")
			if err != nil {
				t.Fatalf("expected no error from Extract, got %v", err)
			}

			// Check if the expected details match the extracted ones
			if len(projectResourceUtilizations) != len(tt.expected) {
				t.Fatalf("expected %d project resource utilizations, got %d", len(tt.expected), len(projectResourceUtilizations))
			}
			// Compare each expected detail with the extracted ones
			if !reflect.DeepEqual(tt.expected, projectResourceUtilizations) {
				t.Errorf("expected %v, got %v", tt.expected, projectResourceUtilizations)
			}
		})
	}
}

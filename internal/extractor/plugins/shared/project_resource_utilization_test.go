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
			name: "should return 0 values when no servers exist for project",
			mockData: []any{
				&identity.Project{ID: "project-123"},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", TotalServers: 0, TotalVCPUsUsed: 0, TotalRAMUsedMB: 0, TotalDiskUsedGB: 0},
			},
		},
		{
			name: "should have values of flavor when only one server exists",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
		{
			name: "should ignore deleted servers",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE"},
				&nova.Server{ID: "server-2", TenantID: "project-123", FlavorName: "flavor-small", Status: "DELETED"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
			},
		},
		{
			name: "should add up values of flavor when multiple servers exists",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},
				&identity.Project{ID: "project-456"},
				&identity.Project{ID: "project-789"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-small", Status: "ACTIVE"},
				&nova.Server{ID: "server-2", TenantID: "project-456", FlavorName: "flavor-small", Status: "ACTIVE"},
				&nova.Server{ID: "server-3", TenantID: "project-456", FlavorName: "flavor-big", Status: "ACTIVE"},

				// Flavors
				&nova.Flavor{ID: "1", Name: "flavor-small", VCPUs: 2, RAM: 4096, Disk: 20},
				&nova.Flavor{ID: "2", Name: "flavor-big", VCPUs: 4, RAM: 8192, Disk: 40},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", TotalServers: 1, TotalVCPUsUsed: 2, TotalRAMUsedMB: 4096, TotalDiskUsedGB: 20},
				{ProjectID: "project-456", TotalServers: 2, TotalVCPUsUsed: 4 + 2, TotalRAMUsedMB: 4096 + 8192, TotalDiskUsedGB: 20 + 40},
				{ProjectID: "project-789", TotalServers: 0, TotalVCPUsUsed: 0, TotalRAMUsedMB: 0, TotalDiskUsedGB: 0},
			},
		},
		{
			name: "should return 0 values when flavor does not exist",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Servers
				&nova.Server{ID: "server-1", TenantID: "project-123", FlavorName: "flavor-nonexistent", Status: "ACTIVE"},
			},
			expected: []ProjectResourceUtilization{
				{ProjectID: "project-123", TotalServers: 1, TotalVCPUsUsed: 0, TotalRAMUsedMB: 0, TotalDiskUsedGB: 0},
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

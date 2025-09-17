// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestProjectResourceCommitmentsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &ProjectResourceCommitmentsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "project_resource_commitments_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(ProjectResourceCommitments{}) {
		t.Error("expected table to be created")
	}
}

func TestProjectResourceCommitmentsExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		mockData []any
		expected []ProjectResourceCommitments
	}{
		{
			name:     "should return empty list when no projects exist",
			mockData: []any{},
			expected: []ProjectResourceCommitments{},
		},
		{
			name: "should return 0 values when no commitments exist for project",
			mockData: []any{
				&identity.Project{ID: "project-123"},
			},
			expected: []ProjectResourceCommitments{
				{ProjectID: "project-123", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 0, TotalRAMCommittedMB: 0, TotalDiskCommittedGB: 0},
			},
		},
		{
			name: "should have values of bare resource commitments when only bare resource commitments exist",
			mockData: []any{
				&identity.Project{ID: "project-123"},
				&identity.Project{ID: "project-456"},
				&identity.Project{ID: "project-789"},

				// Commitments
				&limes.Commitment{ID: 1, ServiceType: "compute", ProjectID: "project-123", ResourceName: "cores", Amount: 10},
				&limes.Commitment{ID: 2, ServiceType: "compute", ProjectID: "project-123", ResourceName: "ram", Amount: 1024},
				&limes.Commitment{ID: 3, ServiceType: "compute", ProjectID: "project-123", ResourceName: "cores", Amount: 20},
				&limes.Commitment{ID: 4, ServiceType: "compute", ProjectID: "project-123", ResourceName: "ram", Amount: 2048},
				&limes.Commitment{ID: 5, ServiceType: "compute", ProjectID: "project-456", ResourceName: "cores", Amount: 10},
				&limes.Commitment{ID: 6, ServiceType: "compute", ProjectID: "project-456", ResourceName: "ram", Amount: 1024},
			},
			expected: []ProjectResourceCommitments{
				{ProjectID: "project-123", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 10 + 20, TotalRAMCommittedMB: 1024 + 2048, TotalDiskCommittedGB: 0},
				{ProjectID: "project-456", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 10, TotalRAMCommittedMB: 1024, TotalDiskCommittedGB: 0},
				{ProjectID: "project-789", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 0, TotalRAMCommittedMB: 0, TotalDiskCommittedGB: 0},
			},
		},
		{
			name: "should use values of flavor when instance commitments exist",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},
				&identity.Project{ID: "project-456"},
				&identity.Project{ID: "project-789"},
				&identity.Project{ID: "project-0"},

				// Commitments
				&limes.Commitment{ID: 1, ServiceType: "compute", ProjectID: "project-123", ResourceName: "instances_small-flavor", Amount: 1},
				&limes.Commitment{ID: 2, ServiceType: "compute", ProjectID: "project-456", ResourceName: "instances_big-flavor", Amount: 1},
				&limes.Commitment{ID: 3, ServiceType: "compute", ProjectID: "project-789", ResourceName: "instances_small-flavor", Amount: 1},
				&limes.Commitment{ID: 4, ServiceType: "compute", ProjectID: "project-789", ResourceName: "instances_big-flavor", Amount: 1},

				// Flavors
				&nova.Flavor{ID: "1", Name: "small-flavor", VCPUs: 2, RAM: 4096, Disk: 20},
				&nova.Flavor{ID: "2", Name: "big-flavor", VCPUs: 4, RAM: 8192, Disk: 40},
			},
			expected: []ProjectResourceCommitments{
				{ProjectID: "project-0", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 0, TotalRAMCommittedMB: 0, TotalDiskCommittedGB: 0},
				{ProjectID: "project-123", TotalInstanceCommitments: 1, TotalVCPUsCommitted: 2, TotalRAMCommittedMB: 4096, TotalDiskCommittedGB: 20},
				{ProjectID: "project-456", TotalInstanceCommitments: 1, TotalVCPUsCommitted: 4, TotalRAMCommittedMB: 8192, TotalDiskCommittedGB: 40},
				{ProjectID: "project-789", TotalInstanceCommitments: 2, TotalVCPUsCommitted: 2 + 4, TotalRAMCommittedMB: 4096 + 8192, TotalDiskCommittedGB: 20 + 40},
			},
		},
		{
			name: "should multiply flavor values with amount of commitments",
			mockData: []any{
				// Projects
				&identity.Project{ID: "project-123"},

				// Commitments
				&limes.Commitment{ID: 1, ServiceType: "compute", ProjectID: "project-123", ResourceName: "instances_small-flavor", Amount: 2},
				// Flavors
				&nova.Flavor{ID: "1", Name: "small-flavor", VCPUs: 2, RAM: 4096, Disk: 20},
			},
			expected: []ProjectResourceCommitments{
				{ProjectID: "project-123", TotalInstanceCommitments: 2, TotalVCPUsCommitted: 2 * 2, TotalRAMCommittedMB: 4096 * 2, TotalDiskCommittedGB: 20 * 2},
			},
		},
		{
			name: "should combine bare and instance commitments",
			mockData: []any{
				&identity.Project{ID: "project-123"},

				// Commitments
				&limes.Commitment{ID: 1, ServiceType: "compute", ProjectID: "project-123", ResourceName: "cores", Amount: 10},
				&limes.Commitment{ID: 2, ServiceType: "compute", ProjectID: "project-123", ResourceName: "ram", Amount: 1024},
				&limes.Commitment{ID: 3, ServiceType: "compute", ProjectID: "project-123", ResourceName: "cores", Amount: 20},
				&limes.Commitment{ID: 4, ServiceType: "compute", ProjectID: "project-123", ResourceName: "ram", Amount: 2048},
				&limes.Commitment{ID: 5, ServiceType: "compute", ProjectID: "project-123", ResourceName: "instances_small-flavor", Amount: 1},
				&limes.Commitment{ID: 6, ServiceType: "compute", ProjectID: "project-123", ResourceName: "instances_big-flavor", Amount: 2},

				// Flavors
				&nova.Flavor{ID: "1", Name: "small-flavor", VCPUs: 2, RAM: 4096, Disk: 20},
				&nova.Flavor{ID: "2", Name: "big-flavor", VCPUs: 4, RAM: 8192, Disk: 40},
			},
			expected: []ProjectResourceCommitments{
				{
					ProjectID:                "project-123",
					TotalInstanceCommitments: 1 + 2, // 1 small + 2 big
					TotalVCPUsCommitted:      10 + 20 + 2*1 + 4*2,
					TotalRAMCommittedMB:      1024 + 2048 + 4096*1 + 8192*2,
					TotalDiskCommittedGB:     0 + 20*1 + 40*2,
				},
			},
		},
		{
			name: "should return 0 values when flavor of instance commitment does not exist",
			mockData: []any{
				&identity.Project{ID: "project-123"},

				// Commitments
				&limes.Commitment{ServiceType: "compute", ID: 5, ProjectID: "project-123", ResourceName: "instance_nonexistent", Amount: 1},
			},
			expected: []ProjectResourceCommitments{
				{
					ProjectID:                "project-123",
					TotalInstanceCommitments: 0,
					TotalVCPUsCommitted:      0,
					TotalRAMCommittedMB:      0,
					TotalDiskCommittedGB:     0,
				},
			},
		},
		{
			name: "ignore other service types than compute",
			mockData: []any{
				&identity.Project{ID: "project-123"},

				// Commitments
				&limes.Commitment{ID: 1, ServiceType: "not-compute", ProjectID: "project-123", ResourceName: "cores", Amount: 10},
			},
			expected: []ProjectResourceCommitments{
				{ProjectID: "project-123", TotalInstanceCommitments: 0, TotalVCPUsCommitted: 0, TotalRAMCommittedMB: 0, TotalDiskCommittedGB: 0},
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
				testDB.AddTable(limes.Commitment{}),
				testDB.AddTable(nova.Flavor{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			extractor := &ProjectResourceCommitmentsExtractor{}
			config := conf.FeatureExtractorConfig{
				Name:           "project_resource_commitments_extractor",
				Options:        conf.NewRawOpts("{}"),
				RecencySeconds: nil, // No recency for this test
			}
			if err := extractor.Init(testDB, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if _, err := extractor.Extract(); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			var projectResourceCommitments []ProjectResourceCommitments
			_, err := testDB.Select(&projectResourceCommitments, "SELECT * FROM "+ProjectResourceCommitments{}.TableName()+" ORDER BY project_id")
			if err != nil {
				t.Fatalf("expected no error from Extract, got %v", err)
			}

			// Check if the expected commitments match the extracted ones
			if len(projectResourceCommitments) != len(tt.expected) {
				t.Fatalf("expected %d project resource commitments, got %d", len(tt.expected), len(projectResourceCommitments))
			}
			// Compare each expected commitment with the extracted ones
			if !reflect.DeepEqual(tt.expected, projectResourceCommitments) {
				t.Errorf("expected %v, got %v", tt.expected, projectResourceCommitments)
			}
		})
	}
}

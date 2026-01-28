// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

func TestVMHostResidencyExtractor_Init(t *testing.T) {
	extractor := &VMHostResidencyExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}
}

func TestVMHostResidencyExtractor_Extract(t *testing.T) {
	// We're using postgres specific syntax here.
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Migration{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	servers := []any{
		&nova.Server{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z"},
		&nova.Server{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00Z"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("failed to insert servers: %v", err)
	}

	migrations := []any{
		&nova.Migration{ID: 1, UUID: "migration1", InstanceUUID: "server1", SourceCompute: "host1", DestCompute: "host2", CreatedAt: "2025-01-03T00:00:00Z", MigrationType: "live-migration"},
		&nova.Migration{ID: 2, UUID: "migration2", InstanceUUID: "server2", SourceCompute: "host2", DestCompute: "host3", CreatedAt: "2025-01-04T00:00:00Z", MigrationType: "resize"},
	}
	if err := testDB.Insert(migrations...); err != nil {
		t.Fatalf("failed to insert migrations: %v", err)
	}

	flavors := []any{
		&nova.Flavor{ID: "flavor1", Name: "small"},
		&nova.Flavor{ID: "flavor2", Name: "medium"},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("failed to insert flavors: %v", err)
	}

	extractor := &VMHostResidencyExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error during extraction, got %v", err)
	}

	if len(features) != 30*3 { // 2 flavors + "all" * 30 buckets
		t.Errorf("expected 90 features, got %d", len(features))
	}

	// Check the actual values of the features
	foundSmall := false
	foundMedium := false
	for _, f := range features {
		bucketFeature, ok := f.(VMHostResidencyHistogramBucket)
		if !ok {
			t.Errorf("feature is not of type VMHostResidencyHistogramBucket: %T", f)
			continue
		}
		if bucketFeature.FlavorName == "small" {
			foundSmall = true
			if bucketFeature.Count == 0 {
				t.Errorf("expected count > 0 for flavor 'small', got %d", bucketFeature.Count)
			}
			if bucketFeature.Sum < 1 {
				t.Errorf("expected sum > 0 for flavor 'small', got %f", bucketFeature.Sum)
			}
		}
		if bucketFeature.FlavorName == "medium" {
			foundMedium = true
			if bucketFeature.Count == 0 {
				t.Errorf("expected count > 0 for flavor 'medium', got %d", bucketFeature.Count)
			}
			if bucketFeature.Sum < 1 {
				t.Errorf("expected sum > 0 for flavor 'medium', got %f", bucketFeature.Sum)
			}
		}
	}
	if !foundSmall {
		t.Errorf("expected feature for flavor 'small' not found")
	}
	if !foundMedium {
		t.Errorf("expected feature for flavor 'medium' not found")
	}
}

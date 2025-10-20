// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVMLifeSpanExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VMLifeSpanHistogramExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_life_span_histogram_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	if !testDB.TableExists(shared.VMLifeSpanHistogramBucket{}) {
		t.Error("expected table to be created")
	}
}

func TestVMLifeSpanExtractor_Extract(t *testing.T) {
	// We're using postgres specific syntax here.
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.DeletedServer{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	servers := []any{
		&nova.DeletedServer{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z", Status: "DELETED", Updated: "2025-01-03T00:00:00Z"},
		&nova.DeletedServer{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00Z", Status: "DELETED", Updated: "2025-01-04T00:00:00Z"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("failed to insert servers: %v", err)
	}

	flavors := []any{
		&nova.Flavor{ID: "flavor1", Name: "small"},
		&nova.Flavor{ID: "flavor2", Name: "medium"},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("failed to insert flavors: %v", err)
	}

	extractor := &VMLifeSpanHistogramExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_life_span_histogram_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
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
		bucketFeature, ok := f.(shared.VMLifeSpanHistogramBucket)
		if !ok {
			t.Errorf("feature is not of type VMLifeSpanHistogramBucket: %T", f)
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

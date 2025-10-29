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
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVMLifeSpanExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VMLifeSpanHistogramExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, &testDB, config); err != nil {
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
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.DeletedServer{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	data := []any{
		// Deleted openstack servers
		&nova.DeletedServer{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z", Status: "DELETED", Updated: "2025-01-03T00:00:00Z"},
		&nova.DeletedServer{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00Z", Status: "DELETED", Updated: "2025-01-04T00:00:00Z"},

		// Non deleted openstack servers
		&nova.Server{ID: "server3", FlavorName: "small", Created: "2025-01-05T00:00:00Z", Status: "ACTIVE"},
		&nova.Server{ID: "server4", FlavorName: "medium", Created: "2025-01-06T00:00:00Z", Status: "SHUTOFF"},

		// Flavors
		&nova.Flavor{ID: "flavor1", Name: "small"},
		&nova.Flavor{ID: "flavor2", Name: "medium"},
	}
	if err := testDB.Insert(data...); err != nil {
		t.Fatalf("failed to insert servers: %v", err)
	}

	extractor := &VMLifeSpanHistogramExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, &testDB, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error during extraction, got %v", err)
	}

	// We expect 30 buckets for each flavor + "all" category
	// 2 flavors + "all" = 3 categories
	// 'detected' and 'running' = 2 states
	expectedLength := 30 * (2 + 1) * 2

	if len(features) != expectedLength {
		t.Errorf("expected %d features, got %d", expectedLength, len(features))
	}

	// Check the actual values of the features
	foundSmallDeleted := false
	foundMediumDeleted := false
	foundSmallRunning := false
	foundMediumRunning := false

	for _, f := range features {
		bucketFeature, ok := f.(shared.VMLifeSpanHistogramBucket)
		if !ok {
			t.Errorf("feature is not of type VMLifeSpanHistogramBucket: %T", f)
			continue
		}

		if bucketFeature.FlavorName == "small" && bucketFeature.Deleted {
			foundSmallDeleted = true
		}
		if bucketFeature.FlavorName == "medium" && bucketFeature.Deleted {
			foundMediumDeleted = true
		}
		if bucketFeature.FlavorName == "small" && !bucketFeature.Deleted {
			foundSmallRunning = true
		}
		if bucketFeature.FlavorName == "medium" && !bucketFeature.Deleted {
			foundMediumRunning = true
		}

		// Check that count and sum are greater than zero for non-empty buckets
		if bucketFeature.Count == 0 {
			t.Errorf("expected count > 0 for flavor 'medium', got %d", bucketFeature.Count)
		}
		if bucketFeature.Sum < 1 {
			t.Errorf("expected sum > 0 for flavor 'medium', got %f", bucketFeature.Sum)
		}
	}
	if !foundSmallDeleted {
		t.Errorf("expected feature for flavor 'small' not found")
	}
	if !foundMediumDeleted {
		t.Errorf("expected feature for flavor 'medium' not found")
	}
	if !foundSmallRunning {
		t.Errorf("expected feature for flavor 'small' not found")
	}
	if !foundMediumRunning {
		t.Errorf("expected feature for flavor 'medium' not found")
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
)

func TestVMHostResidencyExtractor_Extract(t *testing.T) {
	in := VMHostResidencyInputs{
		Servers: []nova.Server{
			{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z"},
			{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00"},
			{ID: "server3", FlavorName: "small", Created: "2025-01-01T00:00:00Z"},
		},
		Migrations: []nova.Migration{
			// Since server creation: 2 days.
			{ID: 1, InstanceUUID: "server1", CreatedAt: "2025-01-03T00:00:00Z", MigrationType: "live-migration"},
			// Since server creation, timestamps without zone: 2 days.
			{ID: 2, InstanceUUID: "server2", CreatedAt: "2025-01-04T00:00:00.000000", MigrationType: "resize"},
			// Since previous migration: 3 days.
			{ID: 3, InstanceUUID: "server3", CreatedAt: "2025-01-05T00:00:00Z"},
			// Since server creation: 1 day.
			{ID: 4, InstanceUUID: "server3", CreatedAt: "2025-01-02T00:00:00Z"},
			// Unparseable timestamp: duration 0.
			{ID: 5, InstanceUUID: "server1", CreatedAt: "invalid"},
			// Vanished server: unknown flavor and duration 0.
			{ID: 6, InstanceUUID: "vanished", CreatedAt: "2025-01-06T00:00:00Z"},
		},
		Flavors: []nova.Flavor{{ID: "flavor1", Name: "small"}, {ID: "flavor2", Name: "medium"}},
	}
	features, err := (&VMHostResidencyExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// 30 buckets for each of small, medium, unknown and all.
	if len(features) != 30*4 {
		t.Fatalf("expected %d features, got %d", 30*4, len(features))
	}
	type stats struct {
		count uint64
		sum   float64
	}
	got := make(map[string]stats)
	for _, f := range features {
		got[f.FlavorName] = stats{f.Count, f.Sum}
	}
	const day = 86400.0
	expected := map[string]stats{
		"small":   {count: 4, sum: (2 + 1 + 3) * day},
		"medium":  {count: 1, sum: 2 * day},
		"unknown": {count: 1, sum: 0},
		"all":     {count: 6, sum: (2 + 1 + 3 + 2) * day},
	}
	for flavor, s := range expected {
		if got[flavor] != s {
			t.Errorf("expected %+v for flavor %s, got %+v", s, flavor, got[flavor])
		}
	}
	isSorted := slices.IsSortedFunc(features, func(a, b VMHostResidencyHistogramBucket) int {
		return cmp.Or(cmp.Compare(a.FlavorName, b.FlavorName), cmp.Compare(a.Bucket, b.Bucket))
	})
	if !isSorted {
		t.Error("expected features to be sorted by flavor and bucket")
	}
}

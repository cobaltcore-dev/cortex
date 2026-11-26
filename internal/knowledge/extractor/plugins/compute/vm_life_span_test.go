// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"slices"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
)

func TestVMLifeSpans(t *testing.T) {
	in := VMLifeSpanInputs{
		DeletedServers: []nova.DeletedServer{
			{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z", Updated: "2025-01-03T00:00:00Z"},
			{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00", Updated: "2025-01-04 00:00:00.000000"},
			// Servers with invalid timestamps are skipped.
			{ID: "server3", FlavorName: "small", Created: "2025-01-02T00:00:00Z", Updated: "invalid"},
		},
		Servers: []nova.Server{
			{ID: "server4", FlavorName: "small", Created: "2025-01-05T00:00:00Z"},
			{ID: "server5", FlavorName: "nonexistent", Created: "2025-01-06T00:00:00Z"},
			{ID: "server6", FlavorName: "small", Created: "invalid"},
		},
		Flavors: []nova.Flavor{{ID: "flavor1", Name: "small"}, {ID: "flavor2", Name: "medium"}},
	}
	now := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	expected := []VMLifeSpanRaw{
		{Duration: 2 * 86400, FlavorName: "small", Deleted: true},
		{Duration: 2 * 86400, FlavorName: "medium", Deleted: true},
		{Duration: 5 * 86400, FlavorName: "small", Deleted: false},
		{Duration: 4 * 86400, FlavorName: "unknown", Deleted: false},
	}
	if got := vmLifeSpans(in, now); !slices.Equal(got, expected) {
		t.Errorf("expected %+v, got %+v", expected, got)
	}
}

func TestVMLifeSpanHistogramExtractor_Extract(t *testing.T) {
	in := VMLifeSpanInputs{
		DeletedServers: []nova.DeletedServer{
			{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z", Updated: "2025-01-03T00:00:00Z"},
			{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00Z", Updated: "2025-01-04T00:00:00Z"},
		},
		Servers: []nova.Server{
			{ID: "server3", FlavorName: "small", Created: "2025-01-05T00:00:00Z", Status: "ACTIVE"},
			{ID: "server4", FlavorName: "medium", Created: "2025-01-06T00:00:00Z", Status: "SHUTOFF"},
		},
		Flavors: []nova.Flavor{{ID: "flavor1", Name: "small"}, {ID: "flavor2", Name: "medium"}},
	}
	features, err := (&VMLifeSpanHistogramExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// 30 buckets for each of (small, medium, all) x (deleted, running).
	if len(features) != 30*3*2 {
		t.Fatalf("expected %d features, got %d", 30*3*2, len(features))
	}
	type key struct {
		flavor  string
		deleted bool
	}
	counts := make(map[key]uint64)
	for _, f := range features {
		counts[key{f.FlavorName, f.Deleted}] = f.Count
	}
	expected := map[key]uint64{
		{"all", false}: 2, {"all", true}: 2,
		{"medium", false}: 1, {"medium", true}: 1,
		{"small", false}: 1, {"small", true}: 1,
	}
	for k, count := range expected {
		if counts[k] != count {
			t.Errorf("expected count %d for %+v, got %d", count, k, counts[k])
		}
	}
	deletedSmallSum := 2 * 86400.0
	for _, f := range features {
		if f.FlavorName == "small" && f.Deleted && f.Sum != deletedSmallSum {
			t.Errorf("expected sum %f for deleted small vms, got %f", deletedSmallSum, f.Sum)
		}
	}
	isSorted := slices.IsSortedFunc(features, func(a, b VMLifeSpanHistogramBucket) int {
		return cmp.Or(cmp.Compare(a.FlavorName, b.FlavorName), compareBool(a.Deleted, b.Deleted), cmp.Compare(a.Bucket, b.Bucket))
	})
	if !isSorted {
		t.Error("expected features to be sorted by flavor, deleted and bucket")
	}
}

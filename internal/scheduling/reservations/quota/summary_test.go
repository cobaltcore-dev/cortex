// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"testing"
)

func TestBuildUsageSummary(t *testing.T) {
	tests := []struct {
		name     string
		usage    map[string]int64
		expected string
	}{
		{
			name:     "nil map",
			usage:    nil,
			expected: "",
		},
		{
			name:     "empty map",
			usage:    map[string]int64{},
			expected: "",
		},
		{
			name: "single group all resources",
			usage: map[string]int64{
				"hw_version_2101_cores":     18,
				"hw_version_2101_instances": 7,
				"hw_version_2101_ram":       21,
			},
			expected: "2101: c=18 i=7 r=21",
		},
		{
			name: "multiple groups sorted alphabetically",
			usage: map[string]int64{
				"hw_version_2152_cores":     4,
				"hw_version_2152_instances": 2,
				"hw_version_2152_ram":       8,
				"hw_version_2101_cores":     18,
				"hw_version_2101_instances": 7,
				"hw_version_2101_ram":       21,
			},
			expected: "2101: c=18 i=7 r=21; 2152: c=4 i=2 r=8",
		},
		{
			name: "only ram set for a single group",
			usage: map[string]int64{
				"hw_version_222_ram": 22,
			},
			expected: "222: r=22",
		},
		{
			name: "only cores set",
			usage: map[string]int64{
				"hw_version_3000_cores": 10,
			},
			expected: "3000: c=10",
		},
		{
			name: "only instances set",
			usage: map[string]int64{
				"hw_version_abc_instances": 5,
			},
			expected: "abc: i=5",
		},
		{
			name: "partial resources across multiple groups",
			usage: map[string]int64{
				"hw_version_2101_ram":       14,
				"hw_version_2152_cores":     6,
				"hw_version_2152_instances": 3,
			},
			expected: "2101: r=14; 2152: c=6 i=3",
		},
		{
			name: "unknown suffix is ignored",
			usage: map[string]int64{
				"hw_version_2101_cores":   18,
				"hw_version_2101_ram":     21,
				"hw_version_2101_disks":   99,
				"hw_version_2101_network": 42,
			},
			expected: "2101: c=18 r=21",
		},
		{
			name: "unknown prefix is ignored",
			usage: map[string]int64{
				"some_random_key":     100,
				"other_prefix_cores":  50,
				"hw_version_2101_ram": 21,
			},
			expected: "2101: r=21",
		},
		{
			name: "no hw_version_ prefix at all returns empty",
			usage: map[string]int64{
				"some_random_key":    100,
				"other_prefix_cores": 50,
				"foo_bar_baz":        1,
			},
			expected: "",
		},
		{
			name: "mixed valid and invalid entries",
			usage: map[string]int64{
				"hw_version_2101_cores":     18,
				"hw_version_2101_instances": 7,
				"hw_version_2101_ram":       21,
				"garbage_key":              999,
				"hw_version_2101_unknown":   42,
				"no_prefix_ram":            11,
			},
			expected: "2101: c=18 i=7 r=21",
		},
		{
			name: "all zero values produce empty output",
			usage: map[string]int64{
				"hw_version_2101_cores":     0,
				"hw_version_2101_instances": 0,
				"hw_version_2101_ram":       0,
			},
			expected: "",
		},
		{
			name: "group name with underscores",
			usage: map[string]int64{
				"hw_version_hana_v2_ram":       64,
				"hw_version_hana_v2_cores":     16,
				"hw_version_hana_v2_instances": 2,
			},
			expected: "hana_v2: c=16 i=2 r=64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildUsageSummary(tt.usage)
			if result != tt.expected {
				t.Errorf("buildUsageSummary() = %q, want %q", result, tt.expected)
			}
		})
	}
}

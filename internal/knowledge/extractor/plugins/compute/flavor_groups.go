// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// FlavorInGroup represents a single flavor within a flavor group.
type FlavorInGroup struct {
	Name        string            `json:"name"`
	VCPUs       uint64            `json:"vcpus"`
	MemoryMB    uint64            `json:"memoryMB"`
	DiskGB      uint64            `json:"diskGB"`
	EphemeralGB uint64            `json:"ephemeralGB,omitempty"`
	ExtraSpecs  map[string]string `json:"extraSpecs,omitempty"`
}

// FlavorGroupFeature represents a flavor group with all its member flavors.
// This is the feature that gets stored in the Knowledge CRD.
type FlavorGroupFeature struct {
	// Name of the flavor group (from hw_version extra_spec)
	Name string `json:"name"`

	// All flavors belonging to this group
	Flavors []FlavorInGroup `json:"flavors"`

	// The largest flavor in the group (used for reservation slot sizing)
	LargestFlavor FlavorInGroup `json:"largestFlavor"`

	// The smallest flavor in the group (used for CR size quantification)
	SmallestFlavor FlavorInGroup `json:"smallestFlavor"`
}

// flavorRow represents a row from the SQL query.
type flavorRow struct {
	Name        string `db:"name"`
	VCPUs       uint64 `db:"vcpus"`
	MemoryMB    uint64 `db:"memory_mb"`
	DiskGB      uint64 `db:"disk"`
	EphemeralGB uint64 `db:"ephemeral"`
	ExtraSpecs  string `db:"extra_specs"`
}

// FlavorGroupExtractor extracts flavor group information from the database.
type FlavorGroupExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},           // No options passed through yaml config
		FlavorGroupFeature, // Feature model
	]
}

//go:embed flavor_groups.sql
var flavorGroupsQuery string

// flavorGroupIdentifierName specifies the extra_spec key used to group flavors.
const flavorGroupIdentifierName = "quota:hw_version"

// Extract flavor groups from the database.
// Groups flavors by their hw_version extra_spec and identifies the largest flavor in each group.
func (e *FlavorGroupExtractor) Extract() ([]plugins.Feature, error) {
	// Query all flavors from database
	var rows []flavorRow
	if _, err := e.DB.Select(&rows, flavorGroupsQuery); err != nil {
		slog.Error("[FlavorGroupExtractor] Failed to query flavors", "error", err)
		return nil, err
	}

	// Group flavors by flavorGroupIdentifierName
	groupMap := make(map[string][]FlavorInGroup)

	for _, row := range rows {
		// Parse extra_specs JSON
		var extraSpecs map[string]string
		if row.ExtraSpecs != "" {
			if err := json.Unmarshal([]byte(row.ExtraSpecs), &extraSpecs); err != nil {
				slog.Info("[FlavorGroupExtractor] Failed to parse extra_specs for flavor", "flavor", row.Name, "error", err)
				continue
			}
		}

		// Extract hw_version from extra_specs
		hwVersion, exists := extraSpecs[flavorGroupIdentifierName]
		if !exists || hwVersion == "" {
			slog.Info("[FlavorGroupExtractor] Flavor missing hw_version extra_spec", "flavor", row.Name)
			continue
		}

		// Add flavor to its group
		flavor := FlavorInGroup{
			Name:        row.Name,
			VCPUs:       row.VCPUs,
			MemoryMB:    row.MemoryMB,
			DiskGB:      row.DiskGB,
			EphemeralGB: row.EphemeralGB,
			ExtraSpecs:  extraSpecs,
		}
		groupMap[hwVersion] = append(groupMap[hwVersion], flavor)
	}

	// Convert map to features
	features := make([]FlavorGroupFeature, 0, len(groupMap))
	for groupName, flavors := range groupMap {
		if len(flavors) == 0 {
			continue
		}

		// Sort flavors by size descending (largest first), tie break by name for consistent ordering
		sort.Slice(flavors, func(i, j int) bool {
			if flavors[i].MemoryMB != flavors[j].MemoryMB {
				return flavors[i].MemoryMB > flavors[j].MemoryMB
			}
			if flavors[i].VCPUs != flavors[j].VCPUs {
				return flavors[i].VCPUs > flavors[j].VCPUs
			}
			return flavors[i].Name < flavors[j].Name
		})

		// Find the largest flavor (highest memory, then highest VCPUs as tiebreaker)
		largest := flavors[0]
		smallest := flavors[len(flavors)-1]

		slog.Info("[FlavorGroupExtractor] Identified largest flavor", "group_name", groupName, "largest_flavor", largest.Name, "memory_mb", largest.MemoryMB, "vcpus", largest.VCPUs,
			"smallest_flavor", smallest.Name, "memory_mb", smallest.MemoryMB, "vcpus", smallest.VCPUs, "based on extra specs", largest.ExtraSpecs)

		features = append(features, FlavorGroupFeature{
			Name:           groupName,
			Flavors:        flavors,
			LargestFlavor:  largest,
			SmallestFlavor: smallest,
		})
	}

	// Sort features by group name for consistent ordering
	sort.Slice(features, func(i, j int) bool {
		return features[i].Name < features[j].Name
	})

	return e.Extracted(features)
}

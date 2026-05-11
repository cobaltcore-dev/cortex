// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	ctrl "sigs.k8s.io/controller-runtime"
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

	// The smallest flavor in the group
	SmallestFlavor FlavorInGroup `json:"smallestFlavor"`

	// RAM-to-core ratio in MiB per vCPU (MemoryMB / VCPUs).
	// If all flavors have the same ratio: RamCoreRatio is set, RamCoreRatioMin/Max are nil.
	// If flavors have different ratios: RamCoreRatio is nil, RamCoreRatioMin/Max are set.
	RamCoreRatio    *uint64 `json:"ramCoreRatio,omitempty"`
	RamCoreRatioMin *uint64 `json:"ramCoreRatioMin,omitempty"`
	RamCoreRatioMax *uint64 `json:"ramCoreRatioMax,omitempty"`
}

func (f *FlavorGroupFeature) HasFixedRamCoreRatio() bool {
	if f.RamCoreRatio == nil {
		return false
	}
	if f.RamCoreRatioMin == nil && f.RamCoreRatioMax == nil {
		return true
	}
	return f.RamCoreRatioMin != nil && f.RamCoreRatioMax != nil &&
		*f.RamCoreRatio == *f.RamCoreRatioMin && *f.RamCoreRatio == *f.RamCoreRatioMax
}

func (f *FlavorGroupFeature) Validate() error {
	hasRatio := f.RamCoreRatio != nil
	hasMin := f.RamCoreRatioMin != nil
	hasMax := f.RamCoreRatioMax != nil

	allThreeSame := hasRatio && hasMin && hasMax &&
		*f.RamCoreRatio == *f.RamCoreRatioMin && *f.RamCoreRatio == *f.RamCoreRatioMax
	isFixed := (hasRatio && !hasMin && !hasMax) || allThreeSame
	isVariable := !hasRatio && hasMin && hasMax
	isNone := !hasRatio && !hasMin && !hasMax

	if !isFixed && !isVariable && !isNone {
		return fmt.Errorf("flavor group %q has inconsistent ratio fields", f.Name)
	}
	if isVariable && *f.RamCoreRatioMin >= *f.RamCoreRatioMax {
		return fmt.Errorf("flavor group %q: RamCoreRatioMin (%d) must be less than RamCoreRatioMax (%d)", f.Name, *f.RamCoreRatioMin, *f.RamCoreRatioMax)
	}
	if (isFixed || isVariable) && f.SmallestFlavor.MemoryMB == 0 {
		return fmt.Errorf("flavor group %q: SmallestFlavor.MemoryMB must be non-zero", f.Name)
	}
	return nil
}

// RAMUnitMiB returns MiB per one declared LIQUID RAM unit:
// fixed-ratio groups use slots (SmallestFlavor.MemoryMB MiB each); variable-ratio use GiB (1024 MiB).
func (f *FlavorGroupFeature) RAMUnitMiB() uint64 {
	if f.HasFixedRamCoreRatio() && f.SmallestFlavor.MemoryMB > 0 {
		return f.SmallestFlavor.MemoryMB
	}
	return 1024
}

func (f *FlavorGroupFeature) DeclaredUnitsToGiB(units int64) int64 {
	return units * int64(f.RAMUnitMiB()) / 1024 //nolint:gosec
}

func (f *FlavorGroupFeature) GiBToDeclaredUnits(gib int64) int64 {
	return gib * 1024 / int64(f.RAMUnitMiB()) //nolint:gosec
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

var flavorGroupLog = ctrl.Log.WithName("flavor_group_extractor")

// Extract flavor groups from the database.
func (e *FlavorGroupExtractor) Extract() ([]plugins.Feature, error) {
	if e.DB == nil {
		return nil, errors.New("database connection is not initialized")
	}

	// Query all flavors from database
	var rows []flavorRow
	if _, err := e.DB.Select(&rows, flavorGroupsQuery); err != nil {
		flavorGroupLog.Error(err, "failed to query flavors")
		return nil, err
	}

	// Group flavors by flavorGroupIdentifierName
	groupMap := make(map[string][]FlavorInGroup)

	for _, row := range rows {
		// Parse extra_specs JSON
		var extraSpecs map[string]string
		if row.ExtraSpecs != "" {
			if err := json.Unmarshal([]byte(row.ExtraSpecs), &extraSpecs); err != nil {
				flavorGroupLog.Info("failed to parse extra_specs for flavor", "flavor", row.Name, "error", err)
				continue
			}
		}

		hwVersion, exists := extraSpecs["quota:hw_version"]
		if !exists || hwVersion == "" {
			flavorGroupLog.Info("flavor missing hw_version extra_spec", "flavor", row.Name)
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

		largest := flavors[0]
		smallest := flavors[len(flavors)-1]

		// Compute RAM/core ratio (MiB per vCPU)
		var minRatio, maxRatio uint64 = ^uint64(0), 0
		for _, f := range flavors {
			if f.VCPUs == 0 {
				continue // Skip flavors with 0 vCPUs to avoid division by zero
			}
			ratio := f.MemoryMB / f.VCPUs
			if ratio < minRatio {
				minRatio = ratio
			}
			if ratio > maxRatio {
				maxRatio = ratio
			}
		}

		var ramCoreRatio, ramCoreRatioMin, ramCoreRatioMax *uint64
		if minRatio == maxRatio && maxRatio != 0 {
			// All flavors have the same ratio
			ramCoreRatio = &minRatio
		} else if maxRatio != 0 {
			// Flavors have different ratios
			ramCoreRatioMin = &minRatio
			ramCoreRatioMax = &maxRatio
		}

		flavorGroupLog.Info("identified largest and smallest flavors",
			"groupName", groupName,
			"largestFlavor", largest.Name,
			"largestMemoryMB", largest.MemoryMB,
			"largestVCPUs", largest.VCPUs,
			"smallestFlavor", smallest.Name,
			"smallestMemoryMB", smallest.MemoryMB,
			"smallestVCPUs", smallest.VCPUs,
			"ramCoreRatio", ramCoreRatio,
			"ramCoreRatioMin", ramCoreRatioMin,
			"ramCoreRatioMax", ramCoreRatioMax)

		fg := FlavorGroupFeature{
			Name:            groupName,
			Flavors:         flavors,
			LargestFlavor:   largest,
			SmallestFlavor:  smallest,
			RamCoreRatio:    ramCoreRatio,
			RamCoreRatioMin: ramCoreRatioMin,
			RamCoreRatioMax: ramCoreRatioMax,
		}
		if err := fg.Validate(); err != nil {
			flavorGroupLog.Error(err, "skipping flavor group with invalid data", "groupName", groupName)
			continue
		}
		features = append(features, fg)
	}

	// Sort features by group name for consistent ordering
	sort.Slice(features, func(i, j int) bool {
		return features[i].Name < features[j].Name
	})

	return e.Extracted(features)
}

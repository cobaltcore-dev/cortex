// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
)

func TestFlavorGroupExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	defer dbEnv.Close()
	testDB := db.DB{DbMap: dbEnv.DbMap}

	// Setup test data - create flavors table
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatal(err)
	}

	// Insert test flavors with quota:hw_version in extra_specs
	// Mix of KVM flavors (should be included) and VMware flavors (should be excluded)
	flavors := []any{
		&nova.Flavor{
			ID:         "1",
			Name:       "hana_c30_m480_v2",
			VCPUs:      30,
			RAM:        491520, // 480GB in MB
			Disk:       100,
			Ephemeral:  0,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","hw:cpu_policy":"dedicated","quota:hw_version":"v2"}`,
		},
		&nova.Flavor{
			ID:         "2",
			Name:       "hana_c60_m960_v2",
			VCPUs:      60,
			RAM:        983040, // 960GB in MB
			Disk:       100,
			Ephemeral:  0,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","hw:cpu_policy":"dedicated","quota:hw_version":"v2"}`,
		},
		&nova.Flavor{
			ID:         "3",
			Name:       "hana_c240_m3840_v2",
			VCPUs:      240,
			RAM:        3932160, // 3840GB in MB
			Disk:       100,
			Ephemeral:  0,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","hw:cpu_policy":"dedicated","hw:numa_nodes":"4","quota:hw_version":"v2"}`,
		},
		&nova.Flavor{
			ID:         "4",
			Name:       "gp_c8_m32_v2",
			VCPUs:      8,
			RAM:        32768, // 32GB in MB
			Disk:       50,
			Ephemeral:  10,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"v2"}`,
		},
		&nova.Flavor{
			ID:         "5",
			Name:       "gp_c16_m64_v2",
			VCPUs:      16,
			RAM:        65536, // 64GB in MB
			Disk:       50,
			Ephemeral:  20,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"v2"}`,
		},
		// VMware flavor - should be excluded from results (filtered by SQL query)
		&nova.Flavor{
			ID:         "6",
			Name:       "vmwa_c32_m512_v1",
			VCPUs:      32,
			RAM:        524288, // 512GB in MB
			Disk:       200,
			Ephemeral:  0,
			ExtraSpecs: `{"capabilities:hypervisor_type":"VMware vCenter Server","quota:hw_version":"v1"}`,
		},
		// Cloud-Hypervisor flavor - should be included (case insensitive)
		&nova.Flavor{
			ID:         "7",
			Name:       "gp_c4_m16_ch",
			VCPUs:      4,
			RAM:        16384, // 16GB in MB
			Disk:       25,
			Ephemeral:  5,
			ExtraSpecs: `{"capabilities:hypervisor_type":"CH","quota:hw_version":"ch"}`,
		},
		// Corner case: Same memory as gp_c8_m32_v2 but MORE VCPUs (should come first)
		&nova.Flavor{
			ID:         "8",
			Name:       "gp_c12_m32_v2",
			VCPUs:      12,
			RAM:        32768, // 32GB in MB - same as gp_c8_m32_v2
			Disk:       50,
			Ephemeral:  10,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"v2"}`,
		},
		// Corner case: Same memory AND same VCPUs as gp_c12_m32_v2 (tests name sorting)
		&nova.Flavor{
			ID:         "9",
			Name:       "gp_c12_m32_alt",
			VCPUs:      12,
			RAM:        32768, // 32GB in MB
			Disk:       50,
			Ephemeral:  10,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"v2"}`,
		},
	}

	if err := testDB.Insert(flavors...); err != nil {
		t.Fatal(err)
	}

	// Create and run extractor
	extractor := &FlavorGroupExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatal(err)
	}

	features, err := extractor.Extract()
	if err != nil {
		t.Fatal(err)
	}

	// Verify results - should be 2 groups (v2 and ch based on hw_version)
	// VMware flavor should be filtered out, Cloud-Hypervisor should be included
	if len(features) != 2 {
		t.Fatalf("expected 2 flavor groups, got %d", len(features))
	}

	// Convert to typed features for easier testing
	var v2Group, chGroup *FlavorGroupFeature
	for _, f := range features {
		fg := f.(FlavorGroupFeature)
		switch fg.Name {
		case "v2":
			v2Group = &fg
		case "ch":
			chGroup = &fg
		}
	}

	// Verify v2 group (contains both HANA and general purpose flavors)
	if v2Group == nil {
		t.Fatal("v2 group not found")
	}
	if len(v2Group.Flavors) != 7 {
		t.Errorf("expected 7 flavors in v2 group (3 HANA + 4 general purpose), got %d", len(v2Group.Flavors))
	}
	// Largest flavor in v2 group should be hana_c240_m3840_v2 (highest memory)
	if v2Group.LargestFlavor.Name != "hana_c240_m3840_v2" {
		t.Errorf("expected largest flavor to be hana_c240_m3840_v2, got %s", v2Group.LargestFlavor.Name)
	}
	if v2Group.LargestFlavor.VCPUs != 240 {
		t.Errorf("expected largest flavor VCPUs to be 240, got %d", v2Group.LargestFlavor.VCPUs)
	}
	if v2Group.LargestFlavor.MemoryMB != 3932160 {
		t.Errorf("expected largest flavor memory to be 3932160 MB, got %d", v2Group.LargestFlavor.MemoryMB)
	}
	if v2Group.LargestFlavor.DiskGB != 100 {
		t.Errorf("expected largest flavor disk to be 100 GB, got %d", v2Group.LargestFlavor.DiskGB)
	}
	if v2Group.LargestFlavor.ExtraSpecs == nil {
		t.Error("expected largest flavor to have extra_specs")
	}
	if v2Group.LargestFlavor.ExtraSpecs["hw:numa_nodes"] != "4" {
		t.Errorf("expected largest flavor to have hw:numa_nodes=4, got %s", v2Group.LargestFlavor.ExtraSpecs["hw:numa_nodes"])
	}
	if v2Group.LargestFlavor.ExtraSpecs["quota:hw_version"] != "v2" {
		t.Errorf("expected largest flavor to have quota:hw_version=v2, got %s", v2Group.LargestFlavor.ExtraSpecs["quota:hw_version"])
	}

	// Verify smallest flavor in v2 group should be gp_c4_m16_ch is NOT in v2, so it's gp_c8_m32_v2 (lowest memory among v2 flavors)
	if v2Group.SmallestFlavor.Name != "gp_c8_m32_v2" {
		t.Errorf("expected smallest flavor to be gp_c8_m32_v2, got %s", v2Group.SmallestFlavor.Name)
	}
	if v2Group.SmallestFlavor.MemoryMB != 32768 {
		t.Errorf("expected smallest flavor memory to be 32768 MB, got %d", v2Group.SmallestFlavor.MemoryMB)
	}
	if v2Group.SmallestFlavor.VCPUs != 8 {
		t.Errorf("expected smallest flavor VCPUs to be 8, got %d", v2Group.SmallestFlavor.VCPUs)
	}

	// Verify Cloud-Hypervisor group
	if chGroup == nil {
		t.Fatal("ch group not found")
	}
	if len(chGroup.Flavors) != 1 {
		t.Errorf("expected 1 flavor in ch group, got %d", len(chGroup.Flavors))
	}
	if chGroup.LargestFlavor.Name != "gp_c4_m16_ch" {
		t.Errorf("expected largest flavor to be gp_c4_m16_ch, got %s", chGroup.LargestFlavor.Name)
	}
	if chGroup.LargestFlavor.ExtraSpecs["quota:hw_version"] != "ch" {
		t.Errorf("expected ch flavor to have quota:hw_version=ch, got %s", chGroup.LargestFlavor.ExtraSpecs["quota:hw_version"])
	}

	// Verify smallest flavor in ch group (only has 1 flavor, so same as largest)
	if chGroup.SmallestFlavor.Name != "gp_c4_m16_ch" {
		t.Errorf("expected smallest flavor to be gp_c4_m16_ch, got %s", chGroup.SmallestFlavor.Name)
	}

	// Generic check: Verify all flavor groups have correctly ordered flavors
	// Flavors must be sorted descending by memory (largest first), with VCPUs as tiebreaker
	for _, f := range features {
		fg := f.(FlavorGroupFeature)

		// Check that flavors are sorted in descending order
		for i := range len(fg.Flavors) - 1 {
			current := fg.Flavors[i]
			next := fg.Flavors[i+1]

			// Primary sort: memory descending
			if current.MemoryMB < next.MemoryMB {
				t.Errorf("Flavors in group %s not sorted by memory: %s (%d MB) should come after %s (%d MB)",
					fg.Name, current.Name, current.MemoryMB, next.Name, next.MemoryMB)
			}

			// Secondary sort: if memory equal, VCPUs descending
			if current.MemoryMB == next.MemoryMB && current.VCPUs < next.VCPUs {
				t.Errorf("Flavors in group %s with equal memory not sorted by VCPUs: %s (%d VCPUs) should come after %s (%d VCPUs)",
					fg.Name, current.Name, current.VCPUs, next.Name, next.VCPUs)
			}
		}

		// Verify LargestFlavor matches the first flavor in sorted list
		if len(fg.Flavors) > 0 && fg.LargestFlavor.Name != fg.Flavors[0].Name {
			t.Errorf("Group %s: LargestFlavor (%s) doesn't match first flavor in sorted list (%s)",
				fg.Name, fg.LargestFlavor.Name, fg.Flavors[0].Name)
		}

		// Verify SmallestFlavor matches the last flavor in sorted list
		if len(fg.Flavors) > 0 && fg.SmallestFlavor.Name != fg.Flavors[len(fg.Flavors)-1].Name {
			t.Errorf("Group %s: SmallestFlavor (%s) doesn't match last flavor in sorted list (%s)",
				fg.Name, fg.SmallestFlavor.Name, fg.Flavors[len(fg.Flavors)-1].Name)
		}
	}

	// Verify that VMware flavor was filtered out
	for _, f := range features {
		fg := f.(FlavorGroupFeature)
		for _, flavor := range fg.Flavors {
			if flavor.Name == "vmwa_c32_m512_v1" {
				t.Errorf("VMware flavor should have been filtered out but was found in group %s", fg.Name)
			}
		}
	}

	// Verify that Cloud-Hypervisor flavor was included in ch group
	foundCH := false
	for _, flavor := range chGroup.Flavors {
		if flavor.Name == "gp_c4_m16_ch" {
			foundCH = true
			if flavor.ExtraSpecs["capabilities:hypervisor_type"] != "CH" {
				t.Errorf("expected CH hypervisor_type, got %s", flavor.ExtraSpecs["capabilities:hypervisor_type"])
			}
			if flavor.ExtraSpecs["quota:hw_version"] != "ch" {
				t.Errorf("expected quota:hw_version=ch, got %s", flavor.ExtraSpecs["quota:hw_version"])
			}
		}
	}
	if !foundCH {
		t.Error("Cloud-Hypervisor flavor should have been included but was not found")
	}

	// Verify RAM/core ratio for v2 group
	// v2 group has flavors with different ratios:
	// - hana flavors: 491520/30=16384, 983040/60=16384, 3932160/240=16384 MiB/vCPU
	// - gp_c8_m32_v2: 32768/8=4096, gp_c16_m64_v2: 65536/16=4096 MiB/vCPU
	// - gp_c12_m32_v2: 32768/12=2730, gp_c12_m32_alt: 32768/12=2730 MiB/vCPU
	// So min=2730, max=16384 (variable ratio)
	if v2Group.RamCoreRatio != nil {
		t.Errorf("expected v2 group to have variable ratio (nil RamCoreRatio), got %d", *v2Group.RamCoreRatio)
	}
	if v2Group.RamCoreRatioMin == nil || *v2Group.RamCoreRatioMin != 2730 {
		var got any = nil
		if v2Group.RamCoreRatioMin != nil {
			got = *v2Group.RamCoreRatioMin
		}
		t.Errorf("expected v2 group RamCoreRatioMin=2730, got %v", got)
	}
	if v2Group.RamCoreRatioMax == nil || *v2Group.RamCoreRatioMax != 16384 {
		var got any = nil
		if v2Group.RamCoreRatioMax != nil {
			got = *v2Group.RamCoreRatioMax
		}
		t.Errorf("expected v2 group RamCoreRatioMax=16384, got %v", got)
	}

	// Verify RAM/core ratio for ch group (single flavor = fixed ratio)
	// gp_c4_m16_ch: 16384/4=4096 MiB/vCPU
	if chGroup.RamCoreRatio == nil || *chGroup.RamCoreRatio != 4096 {
		var got any = nil
		if chGroup.RamCoreRatio != nil {
			got = *chGroup.RamCoreRatio
		}
		t.Errorf("expected ch group RamCoreRatio=4096, got %v", got)
	}
	if chGroup.RamCoreRatioMin != nil {
		t.Errorf("expected ch group RamCoreRatioMin=nil (fixed ratio), got %d", *chGroup.RamCoreRatioMin)
	}
	if chGroup.RamCoreRatioMax != nil {
		t.Errorf("expected ch group RamCoreRatioMax=nil (fixed ratio), got %d", *chGroup.RamCoreRatioMax)
	}
}

func TestFlavorGroupExtractor_RamCoreRatio_FixedRatio(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	defer dbEnv.Close()
	testDB := db.DB{DbMap: dbEnv.DbMap}

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatal(err)
	}

	// Insert flavors with same RAM/core ratio (4096 MiB/vCPU)
	flavors := []any{
		&nova.Flavor{
			ID:         "1",
			Name:       "fixed_c2_m8",
			VCPUs:      2,
			RAM:        8192, // 8GB
			Disk:       50,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"fixed"}`,
		},
		&nova.Flavor{
			ID:         "2",
			Name:       "fixed_c4_m16",
			VCPUs:      4,
			RAM:        16384, // 16GB
			Disk:       50,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"fixed"}`,
		},
		&nova.Flavor{
			ID:         "3",
			Name:       "fixed_c8_m32",
			VCPUs:      8,
			RAM:        32768, // 32GB
			Disk:       50,
			ExtraSpecs: `{"capabilities:hypervisor_type":"qemu","quota:hw_version":"fixed"}`,
		},
	}

	if err := testDB.Insert(flavors...); err != nil {
		t.Fatal(err)
	}

	extractor := &FlavorGroupExtractor{}
	if err := extractor.Init(&testDB, nil, v1alpha1.KnowledgeSpec{}); err != nil {
		t.Fatal(err)
	}

	features, err := extractor.Extract()
	if err != nil {
		t.Fatal(err)
	}

	if len(features) != 1 {
		t.Fatalf("expected 1 flavor group, got %d", len(features))
	}

	fg := features[0].(FlavorGroupFeature)
	if fg.Name != "fixed" {
		t.Errorf("expected group name 'fixed', got %s", fg.Name)
	}

	// All flavors have ratio 4096 MiB/vCPU
	if fg.RamCoreRatio == nil || *fg.RamCoreRatio != 4096 {
		var got any = nil
		if fg.RamCoreRatio != nil {
			got = *fg.RamCoreRatio
		}
		t.Errorf("expected RamCoreRatio=4096, got %v", got)
	}
	if fg.RamCoreRatioMin != nil {
		t.Errorf("expected RamCoreRatioMin=nil for fixed ratio, got %d", *fg.RamCoreRatioMin)
	}
	if fg.RamCoreRatioMax != nil {
		t.Errorf("expected RamCoreRatioMax=nil for fixed ratio, got %d", *fg.RamCoreRatioMax)
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/failover"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Integration Tests
// ============================================================================

func TestIntegration(t *testing.T) {
	lastReconcileTime := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	tests := []IntegrationTestCase{
		{
			Name:         "full reconcile - basic usage",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
				makePQPerAZ("project-b", "az-1", nil),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					// project-a: hana_v2 az-1: (32768+65536)/1024 = 96 GiB, 8+16=24 cores
					// project-a: hana_v2 az-2: 32768/1024 = 32 GiB, 8 cores
					// project-a: general az-1: 4096/1024 = 4 GiB, 2 cores
					// project-b: general az-1: 4096/1024 = 4 GiB, 2 cores
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
					// No CRs -> PaygUsage == TotalUsage
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "full reconcile - with CRs reduces PaygUsage",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				// 2 units of hana_v2 RAM committed in az-1 for project-a
				makeCR("cr-1", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeMemory, v1alpha1.CommitmentStatusConfirmed, int64Ptr(2)),
				// 10 cores committed in az-1 for project-a
				makeCR("cr-2", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeCores, v1alpha1.CommitmentStatusConfirmed, int64Ptr(10)),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
					// PaygUsage = TotalUsage - CRUsage
					// hana_v2 RAM: 96-2=94 in az-1, 32-0=32 in az-2
					// hana_v2 Cores: 24-10=14 in az-1, 8-0=8 in az-2
					// general: no CRs so PaygUsage == TotalUsage
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 94, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 14, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "incremental add - new VM after last reconcile",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			Actions: []TestAction{
				// Step 1: full reconcile to establish baseline
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: HV diff adds a NEW VM (created after last reconcile)
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-new"), // new instance
					}),
					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-new", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z", // far future, always AFTER last reconcile
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"), // 32768 MiB = 32 GiB
								"vcpus":  resource.MustParse("8"),
							},
						},
					),
					// vm-new is created AFTER last reconcile, so it gets incremented
					// +32 GiB RAM (32768/1024), +8 cores
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "incremental add - migration skipped (VM created before last reconcile)",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			Actions: []TestAction{
				// Step 1: full reconcile
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: HV diff adds vm-1 (which was created BEFORE last reconcile = migration)
				{
					Type:  "hv_diff",
					OldHV: makeHV("hv-2", []hv1.Instance{}),
					NewHV: makeHV("hv-2", []hv1.Instance{
						activeInstance("vm-1"), // migrated here, created before reconcile
					}),
					// Should NOT increment -- vm-1 CreatedAt is 2025-12-01 which is before reconcile time
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "incremental remove - deleted VM decrements usage",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			// vm-del is not in VMs (deleted), but has info in DeletedVMs
			DeletedVMs: map[string]*failover.DeletedVMInfo{
				"vm-del": {
					ProjectID:        "project-a",
					FlavorName:       "m1.hana_v2.small",
					AvailabilityZone: "az-1",
					RAMMiB:           32768,
					VCPUs:            8,
				},
			},
			ActiveVMs: map[string]bool{
				"vm-del": false, // not active (truly deleted)
			},
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			Actions: []TestAction{
				// Step 1: full reconcile (vm-del not in VMs so not counted)
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: HV diff removes vm-del (was on HV before, now gone)
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-del"), // was here
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						// vm-del gone
					}),
					// vm-del: IsServerActive=false, deleted info found
					// Decrement: -32 GiB RAM, -8 cores in az-1
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 64, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 16, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "incremental remove - migrated VM not decremented",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ActiveVMs: map[string]bool{
				"vm-1": true, // still active (migrated to another HV)
			},
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// HV reports vm-1 removed (migrated away)
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-2"),
						// vm-1 gone from this HV
					}),
					// vm-1: IsServerActive=true, so NOT decremented
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "CR update triggers PaygUsage recompute",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				makeCR("cr-ram-1", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeMemory, v1alpha1.CommitmentStatusConfirmed, int64Ptr(1)),
			},
			Actions: []TestAction{
				// Step 1: full reconcile with initial CR (UsedAmount=1)
				{
					Type: "full_reconcile",
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 95, "az-2": 32}, // 96-1=95
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: CR UsedAmount increases to 3 -> PaygUsage should drop
				{
					Type:       "cr_update",
					CRName:     "cr-ram-1",
					UsedAmount: 3,
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 93, "az-2": 32}, // 96-3=93
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "unknown flavor VMs are skipped",
			FlavorGroups: testFlavorGroups,
			VMs: []failover.VM{
				{
					UUID: "vm-unknown", FlavorName: "nonexistent-flavor",
					ProjectID: "project-x", AvailabilityZone: "az-1",
					Resources: map[string]resource.Quantity{
						"memory": resource.MustParse("4294967296"),
						"vcpus":  resource.MustParse("2"),
					},
				},
			},
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-x", "az-1", nil),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					// No usage for project-x (unknown flavor skipped)
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-x": {},
					},
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-x": {},
					},
				},
			},
		},
		{
			Name:         "multiple full reconciles are idempotent",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
				makePQPerAZ("project-b", "az-1", nil),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Second full reconcile - same result
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "pending CRs are excluded from PaygUsage deduction",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				// Pending CR should NOT reduce PaygUsage
				makeCR("cr-pending", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeMemory, v1alpha1.CommitmentStatusPending, int64Ptr(5)),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					// PaygUsage == TotalUsage because pending CRs are excluded
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "full reconcile corrects incremental drift",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
			},
			Actions: []TestAction{
				// Step 1: full reconcile establishes correct baseline
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: HV diff adds a short-lived "phantom" VM (created after reconcile,
				// but deleted before the next full reconcile runs). The incremental path
				// bumps TotalUsage by +1 RAM / +8 cores.
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-phantom"),
					}),
					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-phantom", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z", // after last reconcile
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"), // 32768 MiB = 32 GiB
								"vcpus":  resource.MustParse("8"),
							},
						},
					),
					// TotalUsage now has phantom's contribution (drift)
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32}, // 96+32 drift
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},   // 24+8 drift
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 3: full reconcile re-scans all VMs. Reset VM list to baseline
				// (vm-phantom is gone). This corrects the drift back to the ground truth.
				{
					Type:        "full_reconcile",
					OverrideVMs: baseVMsPtr(),
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32}, // corrected
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},  // corrected
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "complex multi-project scenario with adds, removes, and reconcile corrections",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			DeletedVMs: map[string]*failover.DeletedVMInfo{
				"vm-del": {
					ProjectID:        "project-a",
					FlavorName:       "m1.hana_v2.small",
					AvailabilityZone: "az-1",
					RAMMiB:           32768,
					VCPUs:            8,
				},
			},
			ActiveVMs: map[string]bool{
				"vm-del": false, // truly deleted
				"vm-1":   true,  // still active (for migration scenario)
			},
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
				makePQPerAZ("project-b", "az-1", nil),
			},
			Actions: []TestAction{
				// Step 1: full reconcile establishes baseline for both projects
				// project-a hana_v2: az-1=96 GiB / 24 cores, az-2=32 GiB / 8 cores; general: az-1=4 GiB / 2 cores
				// project-b general: az-1=4 GiB / 2 cores
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 2: HV diff adds a genuine new VM to project-a (hana_v2 small, az-1)
				// +32 GiB RAM, +8 cores
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-new-a"),
					}),
					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-new-a", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z",
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"), // 32768 MiB = 32 GiB
								"vcpus":  resource.MustParse("8"),
							},
						},
					),
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 3: HV diff adds a phantom VM to project-b (general, az-1)
				// This is a short-lived VM that will disappear -- DRIFT for project-b
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-2", []hv1.Instance{
						activeInstance("vm-5"),
					}),
					NewHV: makeHV("hv-2", []hv1.Instance{
						activeInstance("vm-5"),
						activeInstance("vm-phantom-b"),
					}),
					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-phantom-b", FlavorName: "m1.general.small",
							ProjectID: "project-b", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z",
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("4294967296"), // 4096 MiB = 4 GiB
								"vcpus":  resource.MustParse("2"),
							},
						},
					),
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-b": {
							"hw_version_general_ram":   {"az-1": 8}, // 4+4 drift
							"hw_version_general_cores": {"az-1": 4}, // 2+2 drift
						},
					},
				},
				// Step 4: HV diff removes vm-del from project-a (truly deleted)
				// -32 GiB RAM, -8 cores in az-1
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-new-a"),
						activeInstance("vm-del"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-new-a"),
					}),

					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-new-a", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z",
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"),
								"vcpus":  resource.MustParse("8"),
							},
						},
					),
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32}, // 128-32=96
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},  // 32-8=24
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 5: full reconcile with OverrideVMs that includes vm-new-a
				// (vm-new-a is now "real" and appears in the VM list).
				// This reconcile:
				//   - project-a: FIXES drift -- truth is 4 (vm-new-a in list), delta said 3
				//   - project-b: FIXES drift -- truth is 1, delta said 2 (phantom gone)
				{
					Type: "full_reconcile",
					OverrideVMs: &[]failover.VM{
						// testVMs + vm-new-a
						testVMs[0], testVMs[1], testVMs[2], testVMs[3], testVMs[4],
						{
							UUID: "vm-new-a", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z",
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"),
								"vcpus":  resource.MustParse("8"),
							},
						},
					},
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32}, // corrected up
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},   // corrected up
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4}, // corrected down
							"hw_version_general_cores": {"az-1": 2}, // corrected down
						},
					},
				},
				// Step 6: another HV diff removes vm-1 from a HV (migration, not deletion).
				// vm-1 is still active (ActiveVMs["vm-1"]=true), so NOT decremented.
				{
					Type: "hv_diff",
					OldHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-1"),
						activeInstance("vm-2"),
						activeInstance("vm-new-a"),
					}),
					NewHV: makeHV("hv-1", []hv1.Instance{
						activeInstance("vm-2"),
						activeInstance("vm-new-a"),
					}),
					OverrideVMs: withExtraVMs(
						failover.VM{
							UUID: "vm-new-a", FlavorName: "m1.hana_v2.small",
							ProjectID: "project-a", AvailabilityZone: "az-1",
							CreatedAt: "2099-01-01T00:00:00Z",
							Resources: map[string]resource.Quantity{
								"memory": resource.MustParse("34359738368"),
								"vcpus":  resource.MustParse("8"),
							},
						},
					),
					// vm-1 migrated, NOT decremented -- totals unchanged
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
				// Step 7: final full reconcile confirms everything matches (no drift).
				// This is the "reconcile that matches the deltas" -- nothing to fix.
				{
					Type: "full_reconcile",
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 128, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 32, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "partial AZ coverage - only az-1 has CRD, az-2 VMs are ignored",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs, // project-a has VMs in az-1 AND az-2
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				// Only az-1 CRD exists — az-2 has VMs but no CRD
				makePQPerAZ("project-a", "az-1", nil),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					// Only az-1 data should be written (az-2 CRD doesn't exist)
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96},
							"hw_version_hana_v2_cores": {"az-1": 24},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96},
							"hw_version_hana_v2_cores": {"az-1": 24},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
		{
			Name:         "total calculation - multi-resource multi-AZ verified",
			FlavorGroups: testFlavorGroups,
			VMs:          testVMs,
			ProjectQuotas: []*v1alpha1.ProjectQuota{
				makePQPerAZ("project-a", "az-1", nil),
				makePQPerAZ("project-a", "az-2", nil),
				makePQPerAZ("project-b", "az-1", nil),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				// 5 GiB hana_v2 RAM committed in az-1, 3 GiB in az-2
				makeCR("cr-ram-az1", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeMemory, v1alpha1.CommitmentStatusConfirmed, int64Ptr(5)),
				makeCR("cr-ram-az2", "project-a", "hana_v2", "az-2",
					v1alpha1.CommittedResourceTypeMemory, v1alpha1.CommitmentStatusConfirmed, int64Ptr(3)),
				// 4 cores committed in az-1
				makeCR("cr-cores-az1", "project-a", "hana_v2", "az-1",
					v1alpha1.CommittedResourceTypeCores, v1alpha1.CommitmentStatusConfirmed, int64Ptr(4)),
			},
			Actions: []TestAction{
				{
					Type: "full_reconcile",
					// Verify TotalUsage is correctly computed from VMs
					ExpectedTotalUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 96, "az-2": 32},
							"hw_version_hana_v2_cores": {"az-1": 24, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
					// Verify PaygUsage = TotalUsage - CRUsage per AZ
					// az-1: hana_v2_ram: 96-5=91, hana_v2_cores: 24-4=20
					// az-2: hana_v2_ram: 32-3=29, hana_v2_cores: 8-0=8
					ExpectedPaygUsage: map[string]map[string]map[string]int64{
						"project-a": {
							"hw_version_hana_v2_ram":   {"az-1": 91, "az-2": 29},
							"hw_version_hana_v2_cores": {"az-1": 20, "az-2": 8},
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
						"project-b": {
							"hw_version_general_ram":   {"az-1": 4},
							"hw_version_general_cores": {"az-1": 2},
						},
					},
				},
			},
		},
	}

	_ = lastReconcileTime // referenced by test data (VM CreatedAt values)

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			env := newIntegrationTestEnv(t, tc)

			for i, action := range tc.Actions {
				t.Logf("  action %d: %s", i+1, action.Type)
				env.executeAction(action)
			}
		})
	}
}

// ============================================================================
// Test Data
// ============================================================================

var testFlavorGroups = map[string]compute.FlavorGroupFeature{
	"hana_v2": {
		Name:           "hana_v2",
		SmallestFlavor: compute.FlavorInGroup{Name: "m1.hana_v2.small", MemoryMB: 32768, VCPUs: 8},
		LargestFlavor:  compute.FlavorInGroup{Name: "m1.hana_v2.large", MemoryMB: 65536, VCPUs: 16},
		Flavors: []compute.FlavorInGroup{
			{Name: "m1.hana_v2.small", MemoryMB: 32768, VCPUs: 8},
			{Name: "m1.hana_v2.large", MemoryMB: 65536, VCPUs: 16},
		},
	},
	"general": {
		Name:           "general",
		SmallestFlavor: compute.FlavorInGroup{Name: "m1.general.small", MemoryMB: 4096, VCPUs: 2},
		LargestFlavor:  compute.FlavorInGroup{Name: "m1.general.small", MemoryMB: 4096, VCPUs: 2},
		Flavors: []compute.FlavorInGroup{
			{Name: "m1.general.small", MemoryMB: 4096, VCPUs: 2},
		},
	},
}

// Standard VM set for most tests.
// project-a has VMs in BOTH flavor groups (hana_v2 and general).
// project-b has only general VMs.
var testVMs = []failover.VM{
	// vm-1: hana_v2, 32 GiB RAM (32768/1024), 8 cores
	{
		UUID: "vm-1", FlavorName: "m1.hana_v2.small",
		ProjectID: "project-a", AvailabilityZone: "az-1",
		CreatedAt: "2025-12-01T00:00:00Z",
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse("34359738368"), // 32768 MiB
			"vcpus":  resource.MustParse("8"),
		},
	},
	// vm-2: hana_v2, 64 GiB RAM (65536/1024), 16 cores
	{
		UUID: "vm-2", FlavorName: "m1.hana_v2.large",
		ProjectID: "project-a", AvailabilityZone: "az-1",
		CreatedAt: "2025-12-01T00:00:00Z",
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse("68719476736"), // 65536 MiB
			"vcpus":  resource.MustParse("16"),
		},
	},
	// vm-3: hana_v2, 32 GiB RAM (32768/1024), 8 cores
	{
		UUID: "vm-3", FlavorName: "m1.hana_v2.small",
		ProjectID: "project-a", AvailabilityZone: "az-2",
		CreatedAt: "2025-12-01T00:00:00Z",
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse("34359738368"), // 32768 MiB
			"vcpus":  resource.MustParse("8"),
		},
	},
	// vm-4: general, 4 GiB RAM (4096/1024), 2 cores
	{
		UUID: "vm-4", FlavorName: "m1.general.small",
		ProjectID: "project-a", AvailabilityZone: "az-1",
		CreatedAt: "2025-12-01T00:00:00Z",
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse("4294967296"), // 4096 MiB
			"vcpus":  resource.MustParse("2"),
		},
	},
	// vm-5: general, 4 GiB RAM (4096/1024), 2 cores
	{
		UUID: "vm-5", FlavorName: "m1.general.small",
		ProjectID: "project-b", AvailabilityZone: "az-1",
		CreatedAt: "2025-12-01T00:00:00Z",
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse("4294967296"), // 4096 MiB
			"vcpus":  resource.MustParse("2"),
		},
	},
}

// ============================================================================
// Integration Test Framework
// ============================================================================

// TestAction defines a single step in an integration test scenario.
type TestAction struct {
	// Type of action to perform.
	// "full_reconcile" - run ReconcilePeriodic
	// "hv_diff" - run ReconcileHVDiff with OldHV/NewHV
	// "cr_update" - update a CR's UsedAmount, then run Reconcile (watch-triggered)
	Type string

	// For hv_diff actions:
	OldHV *hv1.Hypervisor
	NewHV *hv1.Hypervisor

	// OverrideVMs, when non-nil, replaces the VMSource (ListVMs + GetVM) for
	// THIS action and all subsequent actions. Use to simulate VMs appearing or
	// disappearing between steps. To "undo" a temporary VM, set OverrideVMs
	// again in a later action without that VM.
	OverrideVMs *[]failover.VM

	// For cr_update actions:
	CRName     string
	UsedAmount int64

	// Optional: verify state AFTER this action completes.
	// Keys are project IDs. If nil, no verification for this step.
	ExpectedTotalUsage map[string]map[string]map[string]int64
	ExpectedPaygUsage  map[string]map[string]map[string]int64
}

// IntegrationTestCase defines a complete integration test scenario.
type IntegrationTestCase struct {
	Name string

	// Initial state seeded into the fake client and mock VMSource
	VMs        []failover.VM
	DeletedVMs map[string]*failover.DeletedVMInfo // UUID -> deleted VM info
	ActiveVMs  map[string]bool                    // UUID -> IsServerActive response

	FlavorGroups       map[string]compute.FlavorGroupFeature
	ProjectQuotas      []*v1alpha1.ProjectQuota
	CommittedResources []*v1alpha1.CommittedResource

	// Ordered actions with per-step verification
	Actions []TestAction
}

// integrationTestEnv holds the test environment for a single test case.
type integrationTestEnv struct {
	t          *testing.T
	client     client.Client
	controller *QuotaController
	vmSource   *mockVMSource
}

func newIntegrationTestEnv(t *testing.T, tc IntegrationTestCase) *integrationTestEnv {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}

	// Build initial objects list
	var objects []client.Object

	// Create Knowledge CRD with flavor groups
	knowledgeCRD := buildKnowledgeCRD(t, tc.FlavorGroups)
	objects = append(objects, knowledgeCRD)

	// Add ProjectQuotas
	for _, pq := range tc.ProjectQuotas {
		objects = append(objects, pq)
	}

	// Add CommittedResources
	for _, cr := range tc.CommittedResources {
		objects = append(objects, cr)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(
			&v1alpha1.ProjectQuota{},
			&v1alpha1.CommittedResource{},
			&v1alpha1.Knowledge{},
		).
		Build()

	// Build mock VMSource
	vmSrc := &mockVMSource{
		listVMs: func(_ context.Context) ([]failover.VM, error) {
			return tc.VMs, nil
		},
		getVM: func(_ context.Context, vmUUID string) (*failover.VM, error) {
			for i := range tc.VMs {
				if tc.VMs[i].UUID == vmUUID {
					return &tc.VMs[i], nil
				}
			}
			return nil, nil
		},
		isServerActive: func(_ context.Context, vmUUID string) (bool, error) {
			if tc.ActiveVMs != nil {
				if active, ok := tc.ActiveVMs[vmUUID]; ok {
					return active, nil
				}
			}
			return false, nil
		},
		getDeletedVM: func(_ context.Context, vmUUID string) (*failover.DeletedVMInfo, error) {
			if tc.DeletedVMs != nil {
				if info, ok := tc.DeletedVMs[vmUUID]; ok {
					return info, nil
				}
			}
			return nil, nil
		},
	}

	controller := &QuotaController{
		Client:   k8sClient,
		VMSource: vmSrc,
		Config:   DefaultQuotaControllerConfig(),
		Metrics:  NewQuotaMetrics(nil), // no-op metrics
	}

	return &integrationTestEnv{
		t:          t,
		client:     k8sClient,
		controller: controller,
		vmSource:   vmSrc,
	}
}

func (env *integrationTestEnv) verifyTotalUsage(projectID string, expected map[string]map[string]int64) {
	env.t.Helper()

	if expected == nil {
		return
	}

	// Collect expected data per AZ: az → resourceName → value
	perAZ := make(map[string]map[string]int64)
	for resourceName, azMap := range expected {
		for az, val := range azMap {
			if perAZ[az] == nil {
				perAZ[az] = make(map[string]int64)
			}
			perAZ[az][resourceName] = val
		}
	}

	for az, expectedResources := range perAZ {
		crdName := "quota-" + projectID + "-" + az
		var pq v1alpha1.ProjectQuota
		if err := env.client.Get(context.Background(), client.ObjectKey{Name: crdName}, &pq); err != nil {
			env.t.Fatalf("failed to get ProjectQuota %s: %v", crdName, err)
		}

		for resourceName, expectedAmount := range expectedResources {
			actual, ok := pq.Status.TotalUsage[resourceName]
			if !ok {
				env.t.Errorf("project %s AZ %s: expected TotalUsage resource %q not found", projectID, az, resourceName)
				continue
			}
			if actual != expectedAmount {
				env.t.Errorf("project %s: TotalUsage[%s][%s] = %d, want %d",
					projectID, resourceName, az, actual, expectedAmount)
			}
		}

		// Check no unexpected resources
		for resourceName := range pq.Status.TotalUsage {
			if _, ok := expectedResources[resourceName]; !ok {
				env.t.Errorf("project %s AZ %s: unexpected TotalUsage resource %q", projectID, az, resourceName)
			}
		}
	}
}

func (env *integrationTestEnv) verifyPaygUsage(projectID string, expected map[string]map[string]int64) {
	env.t.Helper()

	if expected == nil {
		return
	}

	// Collect expected data per AZ: az → resourceName → value
	perAZ := make(map[string]map[string]int64)
	for resourceName, azMap := range expected {
		for az, val := range azMap {
			if perAZ[az] == nil {
				perAZ[az] = make(map[string]int64)
			}
			perAZ[az][resourceName] = val
		}
	}

	for az, expectedResources := range perAZ {
		crdName := "quota-" + projectID + "-" + az
		var pq v1alpha1.ProjectQuota
		if err := env.client.Get(context.Background(), client.ObjectKey{Name: crdName}, &pq); err != nil {
			env.t.Fatalf("failed to get ProjectQuota %s: %v", crdName, err)
		}

		for resourceName, expectedAmount := range expectedResources {
			actual, ok := pq.Status.PaygUsage[resourceName]
			if !ok {
				env.t.Errorf("project %s AZ %s: expected PaygUsage resource %q not found", projectID, az, resourceName)
				continue
			}
			if actual != expectedAmount {
				env.t.Errorf("project %s: PaygUsage[%s][%s] = %d, want %d",
					projectID, resourceName, az, actual, expectedAmount)
			}
		}

		for resourceName := range pq.Status.PaygUsage {
			if _, ok := expectedResources[resourceName]; !ok {
				env.t.Errorf("project %s AZ %s: unexpected PaygUsage resource %q", projectID, az, resourceName)
			}
		}
	}
}

func (env *integrationTestEnv) executeAction(action TestAction) {
	env.t.Helper()
	ctx := context.Background()

	// Apply OverrideVMs if set (persists for all subsequent actions)
	if action.OverrideVMs != nil {
		vms := *action.OverrideVMs
		env.vmSource.listVMs = func(_ context.Context) ([]failover.VM, error) {
			return vms, nil
		}
		env.vmSource.getVM = func(_ context.Context, vmUUID string) (*failover.VM, error) {
			for i := range vms {
				if vms[i].UUID == vmUUID {
					return &vms[i], nil
				}
			}
			return nil, nil
		}
	}

	switch action.Type {
	case "full_reconcile":
		if err := env.controller.ReconcilePeriodic(ctx); err != nil {
			env.t.Fatalf("ReconcilePeriodic failed: %v", err)
		}

	case "hv_diff":
		if err := env.controller.ReconcileHVDiff(ctx, action.OldHV, action.NewHV); err != nil {
			env.t.Fatalf("ReconcileHVDiff failed: %v", err)
		}

	case "cr_update":
		// Fetch the CR, update UsedResources, then call Reconcile
		var cr v1alpha1.CommittedResource
		if err := env.client.Get(ctx, client.ObjectKey{Name: action.CRName}, &cr); err != nil {
			env.t.Fatalf("failed to get CR %s: %v", action.CRName, err)
		}
		cr.Status.UsedResources = usedResourcesFromMultiples(cr.Spec.ResourceType, cr.Spec.FlavorGroupName, action.UsedAmount)
		if err := env.client.Status().Update(ctx, &cr); err != nil {
			env.t.Fatalf("failed to update CR %s status: %v", action.CRName, err)
		}

		// Simulate watch trigger: call Reconcile for the affected per-AZ CRD
		pqName := "quota-" + cr.Spec.ProjectID + "-" + cr.Spec.AvailabilityZone
		_, err := env.controller.Reconcile(ctx, reconcileRequest(pqName))
		if err != nil {
			env.t.Fatalf("Reconcile failed after CR update: %v", err)
		}

	default:
		env.t.Fatalf("unknown action type: %s", action.Type)
	}

	// Verify expected state after this action
	if action.ExpectedTotalUsage != nil {
		for projectID, expected := range action.ExpectedTotalUsage {
			env.verifyTotalUsage(projectID, expected)
		}
	}
	if action.ExpectedPaygUsage != nil {
		for projectID, expected := range action.ExpectedPaygUsage {
			env.verifyPaygUsage(projectID, expected)
		}
	}
}

// ============================================================================
// Helpers
// ============================================================================

func buildKnowledgeCRD(t *testing.T, flavorGroups map[string]compute.FlavorGroupFeature) *v1alpha1.Knowledge {
	t.Helper()

	// Convert map to slice for BoxFeatureList
	var features []compute.FlavorGroupFeature
	for _, fg := range flavorGroups {
		features = append(features, fg)
	}

	raw, err := boxFlavorGroupFeatures(features)
	if err != nil {
		t.Fatalf("failed to box flavor group features: %v", err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "flavor-groups",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: "nova",
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw: raw,
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.KnowledgeConditionReady,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "Ready",
				},
			},
		},
	}
}

func boxFlavorGroupFeatures(features []compute.FlavorGroupFeature) (runtime.RawExtension, error) {
	rawSerialized := struct {
		Features []compute.FlavorGroupFeature `json:"features"`
	}{
		Features: features,
	}
	data, err := json.Marshal(rawSerialized)
	if err != nil {
		return runtime.RawExtension{}, err
	}
	return runtime.RawExtension{Raw: data}, nil
}

func reconcileRequest(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Name: name}}
}

func makePQ(projectID string, lastReconcileAt *metav1.Time) *v1alpha1.ProjectQuota { //nolint:unused
	return &v1alpha1.ProjectQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "quota-" + projectID},
		Spec:       v1alpha1.ProjectQuotaSpec{ProjectID: projectID, DomainID: "domain-1"},
		Status: v1alpha1.ProjectQuotaStatus{
			LastReconcileAt: lastReconcileAt,
		},
	}
}

// makePQPerAZ creates a per-AZ ProjectQuota CRD for integration tests.
func makePQPerAZ(projectID, az string, lastReconcileAt *metav1.Time) *v1alpha1.ProjectQuota { //nolint:unparam
	return &v1alpha1.ProjectQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "quota-" + projectID + "-" + az},
		Spec: v1alpha1.ProjectQuotaSpec{
			ProjectID:        projectID,
			DomainID:         "domain-1",
			AvailabilityZone: az,
		},
		Status: v1alpha1.ProjectQuotaStatus{
			LastReconcileAt: lastReconcileAt,
		},
	}
}

func makeCR(name, projectID, flavorGroup, az string, resourceType v1alpha1.CommittedResourceType, state v1alpha1.CommitmentStatus, usedAmount *int64) *v1alpha1.CommittedResource { //nolint:unparam
	cr := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   name + "-uuid",
			FlavorGroupName:  flavorGroup,
			ResourceType:     resourceType,
			AvailabilityZone: az,
			ProjectID:        projectID,
			DomainID:         "domain-1",
			Amount:           resource.MustParse("10"),
			State:            state,
		},
	}
	if usedAmount != nil {
		cr.Status.UsedResources = usedResourcesFromMultiples(resourceType, flavorGroup, *usedAmount)
	}
	return cr
}

// usedResourcesFromMultiples converts a "multiples" value (the old UsedAmount unit) to UsedResources.
// For memory: multiples * 1 GiB = bytes.
// For cores: the value is used directly.
func usedResourcesFromMultiples(resourceType v1alpha1.CommittedResourceType, flavorGroup string, multiples int64) map[string]resource.Quantity {
	switch resourceType {
	case v1alpha1.CommittedResourceTypeMemory:
		if _, ok := testFlavorGroups[flavorGroup]; !ok {
			return nil
		}
		bytesVal := multiples * 1024 * 1024 * 1024
		return map[string]resource.Quantity{
			"memory": *resource.NewQuantity(bytesVal, resource.BinarySI),
		}
	case v1alpha1.CommittedResourceTypeCores:
		return map[string]resource.Quantity{
			"cpu": *resource.NewQuantity(multiples, resource.DecimalSI),
		}
	default:
		return nil
	}
}

func int64Ptr(v int64) *int64 { return &v }

// withExtraVMs returns a pointer to testVMs + additional VMs.
// Used with OverrideVMs to add VMs to the "world" for an action.
func withExtraVMs(extra ...failover.VM) *[]failover.VM {
	vms := append(append([]failover.VM{}, testVMs...), extra...)
	return &vms
}

// baseVMsPtr returns a pointer to a copy of testVMs (resets to baseline).
func baseVMsPtr() *[]failover.VM {
	vms := append([]failover.VM{}, testVMs...)
	return &vms
}

func makeHV(name string, instances []hv1.Instance) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			Instances: instances,
		},
	}
}

func activeInstance(id string) hv1.Instance {
	return hv1.Instance{ID: id, Active: true}
}

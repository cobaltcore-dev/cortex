// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"maps"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// intentFromHint maps an old "_nova_check_type" scheduler hint value to the
// typed scheduling intent the plugin now reads. The hints map carries at most
// the "_nova_check_type" key in these tests.
func intentFromHint(hints map[string]any) v1alpha1.SchedulingIntent {
	raw, ok := hints["_nova_check_type"]
	if !ok {
		return ""
	}
	switch raw {
	case "evacuate":
		return api.EvacuateIntent
	case "live_migrate":
		return api.LiveMigrationIntent
	case "reserve_for_failover":
		return api.ReserveForFailoverIntent
	case "reserve_for_committed_resource":
		return api.ReserveForCommittedResourceIntent
	case "resize":
		return api.ResizeIntent
	case "capacity_probe":
		return api.CapacityProbeIntent
	default:
		return ""
	}
}

func makeQuotaEnforcementRequest(projectID, az, hwVersion string, memoryMB, numInstances uint64, hints map[string]any) compute.PlacementRequest {
	return compute.PlacementRequest{
		ProjectID:        projectID,
		AvailabilityZone: az,
		NumInstances:     numInstances,
		Intent:           intentFromHint(hints),
		HardwareVersion:  hwVersion,
		Flavor: compute.FlavorSpec{
			VCPUs:    4,
			MemoryMB: memoryMB,
		},
		Hosts: []compute.Host{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}
}

func TestFilterQuotaEnforcement_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}

	type tc struct {
		name    string
		objects []client.Object
		request compute.PlacementRequest
		enforce bool
		// crStateFilter sets Options.CRStateFilter; nil exercises the default (confirmed).
		crStateFilter []v1alpha1.CommitmentStatus

		expectAccept bool

		// Metric expectations — every case asserts exactly one increment on the
		// labeled series and exactly one series in the vector.
		expectMode     string // "enforce" | "shadow"
		expectDecision string // accept_cr | accept_payg | accept_no_quota | accept_skipped_intent_migration | accept_skipped_intent_internal | accept_skipped_missing_data | reject
		expectResource string // "ram" | "cores" | "instances" | ""
		expectAZ       string
		expectFG       string
	}

	tests := []tc{
		{
			name: "ACCEPT: CR has headroom",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")}},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_cr",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: CR has exact headroom (confirmed state)",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("90Gi")}},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_cr",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: PAYG has headroom (no CR headroom)",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("50Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")}},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_hana_v2_ram":       200,
							"hw_version_hana_v2_cores":     100,
							"hw_version_hana_v2_instances": 100,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 100},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: no CR headroom and no PAYG headroom",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("50Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")}},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 100},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 50},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: PAYG headroom negative",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 50},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 60},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name:           "ACCEPT: no ProjectQuota CRD found (skip enforcement)",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_no_quota",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name:    "SKIP: evacuate intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "evacuate"}),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_intent_migration",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
		{
			name:    "SKIP: live migration intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "live_migrate"}),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_intent_migration",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
		{
			name:    "SKIP: reserve_for_failover intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "reserve_for_failover"}),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_intent_internal",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
		{
			name:    "SKIP: reserve_for_committed_resource intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "reserve_for_committed_resource"}),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_intent_internal",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
		{
			name:           "SKIP: no hw_version in flavor",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_missing_data",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "",
		},
		{
			name:           "SKIP: no project ID",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_missing_data",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name:           "SKIP: no availability zone",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("project-1", "", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped_missing_data",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: CR from different project does not provide headroom",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-other-project"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-OTHER",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("0")}},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_hana_v2_ram":       5,
							"hw_version_hana_v2_cores":     100,
							"hw_version_hana_v2_instances": 100,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 5},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: CR in different AZ doesn't count",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-other-az"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-2",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("0")}},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 5},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 5},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "CR in planned state is ignored",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-planned"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusPlanned,
						Amount:           resource.MustParse("100Gi"),
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 5},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 5},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "confirmed CR is deducted from PAYG headroom (reject)",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-confirmed-full"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					// Fully used, so it provides no CR headroom itself — the request
					// falls through to the PAYG check where its amount is deducted.
					Status: v1alpha1.CommittedResourceStatus{
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("100Gi")},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 150},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 45},
					},
				},
			},
			// headroom = 150 - 100 (confirmed CR) - 45 = 5 < 10 requested → reject.
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "guaranteed CR is excluded from PAYG headroom under default filter (accept)",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-guaranteed-full"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusGuaranteed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("100Gi")},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 150},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 45},
					},
				},
			},
			// Default filter (confirmed only) excludes the guaranteed CR:
			// headroom = 150 - 0 - 45 = 105 >= 10 requested → accept via PAYG.
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: multiple instances, enough PAYG headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_hana_v2_ram":       500,
							"hw_version_hana_v2_cores":     100,
							"hw_version_hana_v2_instances": 100,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 100},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 3, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: multiple instances exceed PAYG headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 120},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 100},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 3, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: resize intent is enforced (not skipped)",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 10},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 10},
					},
				},
			},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "resize"}),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "CR cores type is ignored for memory check",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-cores"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeCores,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100"),
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 5},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 5},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: different hw_version resource name (vmware_v2)",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_vmware_v2_ram":       500,
							"hw_version_vmware_v2_cores":     100,
							"hw_version_vmware_v2_instances": 100,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_vmware_v2_ram": 100},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "vmware_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "vmware_v2",
		},
		{
			name: "ACCEPT: RAM quota has headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 200},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 50},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: RAM quota exceeded",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 55},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 50},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: cores quota has headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_cores": 100},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_cores": 10},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: cores quota exceeded",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_cores": 5},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_cores": 3},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "cores",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: instances quota has headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_instances": 10},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_instances": 5},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_payg",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: instances quota exceeded",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_instances": 3},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_instances": 3},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "instances",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: CR headroom bypasses PAYG cores rejection",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-ram"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						Billing: &v1alpha1.BillingStatus{UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")}},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_cores": 2},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_cores": 2},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "accept_cr",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "REJECT: small flavor still rejected when no headroom",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 1},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 1},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 2048, 1, nil),
			expectAccept:   false,
			enforce:        true,
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		// Shadow-mode cases.
		{
			name: "SHADOW: would reject RAM but enforce=false preserves activations",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_ram": 5},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 0},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "SHADOW: would reject cores but enforce=false preserves activations",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_cores": 3},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_cores": 3},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "reject",
			expectResource: "cores",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "SHADOW: would reject instances but enforce=false preserves activations",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota:            map[string]int64{"hw_version_hana_v2_instances": 1},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_instances": 1},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "reject",
			expectResource: "instances",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "SHADOW: CR accept records mode=shadow",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusConfirmed,
						Amount:           resource.MustParse("100Gi"),
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "accept_cr",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name:    "SHADOW: skip intent records mode=shadow",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "live_migrate"}),
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "accept_skipped_intent_migration",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			filter := &FilterQuotaEnforcement{}
			filter.Client = fakeClient
			filter.Options.Enforce = tt.enforce
			filter.Options.CRStateFilter = tt.crStateFilter

			result, err := filter.Run(logr.Discard(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			numHosts := len(tt.request.Hosts)
			if tt.expectAccept {
				if len(result.Activations) != numHosts {
					t.Errorf("expected all %d hosts to be accepted, got %d activations",
						numHosts, len(result.Activations))
				}
			} else {
				if len(result.Activations) != 0 {
					t.Errorf("expected 0 activations (reject), got %d", len(result.Activations))
				}
			}

			// Event assertions: exactly one quota_enforcement_decision event with
			// the expected label set (no stray events).
			assertDecisionEvent(t, result.Events,
				tt.expectMode, tt.expectDecision, tt.expectResource, tt.expectAZ, tt.expectFG)
		})
	}
}

// assertDecisionEvent checks that events contains exactly one
// quota_enforcement_decision event carrying the expected labels.
func assertDecisionEvent(t *testing.T, events []lib.FilterWeigherPipelineStepEvent, mode, decision, resource, az, flavorGroup string) {
	t.Helper()
	want := map[string]string{
		"mode":              mode,
		"decision":          decision,
		"resource":          resource,
		"availability_zone": az,
		"flavor_group":      flavorGroup,
	}
	var matches int
	for _, ev := range events {
		if ev.Name != "quota_enforcement_decision" {
			continue
		}
		matches++
		if !maps.Equal(ev.Labels, want) {
			t.Errorf("event labels = %v, want %v", ev.Labels, want)
		}
	}
	if matches != 1 {
		t.Errorf("expected exactly 1 quota_enforcement_decision event, got %d (events: %v)", matches, events)
	}
}

// TestFilterQuotaEnforcement_DefaultIsShadow guards the operational invariant
// that a freshly-constructed FilterQuotaEnforcement (zero-value Options) runs
// in shadow mode. The pipeline yaml intentionally does not set the `enforce`
// parameter so that newly-rolled-out releases never silently start rejecting
// requests; this test fails if anyone flips the default back to enforce.
func TestFilterQuotaEnforcement_DefaultIsShadow(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}

	// ProjectQuota that the request would exceed: 5 GiB quota, 0 used, request 10 GiB.
	objects := []client.Object{
		&v1alpha1.ProjectQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
			Spec: v1alpha1.ProjectQuotaSpec{
				ProjectID:        "project-1",
				AvailabilityZone: "az-1",
				Quota:            map[string]int64{"hw_version_hana_v2_ram": 5},
			},
			Status: v1alpha1.ProjectQuotaStatus{
				PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 0},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	// Construct with zero-value Options — no Enforce set.
	filter := &FilterQuotaEnforcement{}
	filter.Client = fakeClient

	if filter.Options.Enforce {
		t.Fatalf("zero-value FilterQuotaEnforcementOpts.Enforce must be false (shadow), got true")
	}

	request := makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil)
	result, err := filter.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Shadow mode preserves all activations even though the request exceeds quota.
	if len(result.Activations) != len(request.Hosts) {
		t.Errorf("expected all %d hosts to remain activated in shadow mode, got %d",
			len(request.Hosts), len(result.Activations))
	}

	// Decision event must record mode=shadow with a reject decision.
	assertDecisionEvent(t, result.Events, "shadow", "reject", "ram", "az-1", "hana_v2")
}

func TestQuantityToGiB(t *testing.T) {
	tests := []struct {
		name     string
		quantity resource.Quantity
		expected int64
	}{
		{name: "100Gi exact", quantity: resource.MustParse("100Gi"), expected: 100},
		{name: "1Ti = 1024 GiB", quantity: resource.MustParse("1Ti"), expected: 1024},
		{name: "512Mi = 1 GiB (ceil)", quantity: resource.MustParse("512Mi"), expected: 1},
		{name: "1Gi exact", quantity: resource.MustParse("1Gi"), expected: 1},
		{name: "0 bytes", quantity: resource.MustParse("0"), expected: 0},
		{name: "1.5Gi = 2 GiB (ceil)", quantity: resource.MustParse("1536Mi"), expected: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quantityToGiB(tt.quantity); got != tt.expected {
				t.Errorf("quantityToGiB(%v) = %d, want %d", tt.quantity, got, tt.expected)
			}
		})
	}
}

func TestFilterQuotaEnforcement_SkipsForNonPlacementIntent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	// Enforce mode: quota fully consumed → all hosts would normally be filtered.
	objects := []client.Object{
		&v1alpha1.ProjectQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "quota-project-no-quota-az-1"},
			Spec: v1alpha1.ProjectQuotaSpec{
				ProjectID:        "project-no-quota",
				AvailabilityZone: "az-1",
				Quota:            map[string]int64{"hw_version_gp_ram": 10, "hw_version_gp_cores": 10, "hw_version_gp_instances": 1},
			},
			Status: v1alpha1.ProjectQuotaStatus{
				PaygUsage: map[string]int64{"hw_version_gp_ram": 10, "hw_version_gp_cores": 10, "hw_version_gp_instances": 1},
			},
		},
	}
	request := makeQuotaEnforcementRequest("project-no-quota", "az-1", "gp", 1, 1, map[string]any{"_nova_check_type": "capacity_probe"})
	step := &FilterQuotaEnforcement{}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	step.Options = FilterQuotaEnforcementOpts{Enforce: true}

	result, err := step.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != len(request.Hosts) {
		t.Errorf("expected all %d hosts to pass, got %d", len(request.Hosts), len(result.Activations))
	}
}

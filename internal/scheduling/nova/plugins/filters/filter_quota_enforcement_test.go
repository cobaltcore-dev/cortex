// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func makeQuotaEnforcementRequest(projectID, az, hwVersion string, memoryMB, numInstances uint64, hints map[string]any) api.ExternalSchedulerRequest {
	flavor := api.NovaFlavor{
		MemoryMB: memoryMB,
		VCPUs:    4,
	}
	if hwVersion != "" {
		flavor.ExtraSpecs = map[string]string{"hw_version": hwVersion}
	} else {
		flavor.ExtraSpecs = map[string]string{}
	}
	return api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				ProjectID:        projectID,
				AvailabilityZone: az,
				NumInstances:     numInstances,
				SchedulerHints:   hints,
				Flavor:           api.NovaObject[api.NovaFlavor]{Data: flavor},
			},
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}
}

// installFreshMetrics swaps in a fresh QuotaEnforcementMetrics for the duration
// of a test and restores the previous singleton afterwards. Each case starts
// from a known-zero state and is fully isolated.
func installFreshMetrics(t *testing.T) *QuotaEnforcementMetrics {
	t.Helper()
	prev := QuotaEnforcementMetricsSingleton
	m := NewQuotaEnforcementMetrics()
	QuotaEnforcementMetricsSingleton = m
	t.Cleanup(func() { QuotaEnforcementMetricsSingleton = prev })
	return m
}

func TestFilterQuotaEnforcement_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}

	type tc struct {
		name    string
		objects []client.Object
		request api.ExternalSchedulerRequest
		dryRun  bool

		expectAccept bool

		// Metric expectations — every case asserts exactly one increment on the
		// labeled series and exactly one series in the vector.
		expectMode     string // "enforce" | "shadow"
		expectDecision string // accept_cr | accept_payg | accept_no_quota | accept_skipped | reject
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "enforce",
			expectDecision: "accept_cr",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "ACCEPT: CR has exact headroom (guaranteed state)",
			objects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
					Spec: v1alpha1.CommittedResourceSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						FlavorGroupName:  "hana_v2",
						ResourceType:     v1alpha1.CommittedResourceTypeMemory,
						State:            v1alpha1.CommitmentStatusGuaranteed,
						Amount:           resource.MustParse("100Gi"),
					},
					Status: v1alpha1.CommittedResourceStatus{
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("90Gi")},
					},
				},
			},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")},
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")},
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
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
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
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
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
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
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
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
		{
			name:           "SKIP: no hw_version in flavor",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("project-1", "az-1", "", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "",
		},
		{
			name:           "SKIP: no project ID",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
			expectResource: "",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name:           "SKIP: no availability zone",
			objects:        []client.Object{},
			request:        makeQuotaEnforcementRequest("project-1", "", "hana_v2", 10240, 1, nil),
			expectAccept:   true,
			expectMode:     "enforce",
			expectDecision: "accept_skipped",
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("0")},
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("0")},
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
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
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
						UsedResources: map[string]resource.Quantity{"memory": resource.MustParse("50Gi")},
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
			expectMode:     "enforce",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		// Shadow-mode cases.
		{
			name: "SHADOW: would reject RAM but dryRun preserves activations",
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
			dryRun:         true,
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "reject",
			expectResource: "ram",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "SHADOW: would reject cores but dryRun preserves activations",
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
			dryRun:         true,
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "reject",
			expectResource: "cores",
			expectAZ:       "az-1",
			expectFG:       "hana_v2",
		},
		{
			name: "SHADOW: would reject instances but dryRun preserves activations",
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
			dryRun:         true,
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
			dryRun:         true,
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
			dryRun:         true,
			expectAccept:   true,
			expectMode:     "shadow",
			expectDecision: "accept_skipped",
			expectResource: "",
			expectAZ:       "",
			expectFG:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installFreshMetrics(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			filter := &FilterQuotaEnforcement{}
			filter.Client = fakeClient
			filter.Options.DryRun = tt.dryRun

			result, err := filter.Run(slog.Default(), tt.request)
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

			// Metric assertions: exactly one increment on the expected label set
			// and exactly one series in the vector (no stray increments).
			got := testutil.ToFloat64(m.Decisions.WithLabelValues(
				tt.expectMode, tt.expectDecision, tt.expectResource, tt.expectAZ, tt.expectFG,
			))
			if got != 1 {
				t.Errorf("expected 1 increment for mode=%q decision=%q resource=%q az=%q flavor_group=%q; got %v",
					tt.expectMode, tt.expectDecision, tt.expectResource, tt.expectAZ, tt.expectFG, got)
			}
			if n := testutil.CollectAndCount(m.Decisions); n != 1 {
				t.Errorf("expected exactly 1 metric series in vector, got %d", n)
			}
		})
	}
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

func TestNewQuotaEnforcementMetrics(t *testing.T) {
	m := NewQuotaEnforcementMetrics()
	if m == nil || m.Decisions == nil {
		t.Fatal("expected non-nil metrics with non-nil Decisions vec")
	}
	// The constructor no longer registers, so the caller is responsible for
	// registering with a registry. Verify *QuotaEnforcementMetrics implements
	// prometheus.Collector by registering it.
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	// Increment on a label set; verify it lands.
	m.RecordDecision("shadow", "reject", "ram", "az-1", "hana_v2")
	got := testutil.ToFloat64(m.Decisions.WithLabelValues("shadow", "reject", "ram", "az-1", "hana_v2"))
	if got != 1 {
		t.Errorf("expected 1 after RecordDecision, got %v", got)
	}
	// Re-registering the same collector must fail (proves it was registered).
	if err := reg.Register(m); err == nil {
		t.Error("expected error re-registering already-registered metric")
	}
}

func TestQuotaEnforcementMetrics_RecordDecision_NilWarns(t *testing.T) {
	// Capture slog output to confirm the nil-receiver path warns exactly once,
	// even when called many times. We freshly arm the package-level sync.Once
	// so this test deterministically covers the warn path regardless of test
	// ordering.
	origOnce := recordDecisionNilOnce
	recordDecisionNilOnce = &sync.Once{}
	t.Cleanup(func() { recordDecisionNilOnce = origOnce })

	var buf bytes.Buffer
	origDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	var nilMetrics *QuotaEnforcementMetrics
	// Must not panic, and must warn exactly once across multiple calls.
	nilMetrics.RecordDecision("enforce", "reject", "ram", "az-1", "hana_v2")
	nilMetrics.RecordDecision("enforce", "reject", "ram", "az-1", "hana_v2")

	const msg = "QuotaEnforcementMetrics is nil"
	if got := strings.Count(buf.String(), msg); got != 1 {
		t.Errorf("expected warn message %q to appear exactly once, got %d; output: %q",
			msg, got, buf.String())
	}
}

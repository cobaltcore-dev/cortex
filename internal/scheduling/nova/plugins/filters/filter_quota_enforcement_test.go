// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func makeQuotaEnforcementRequest(projectID, az, hwVersion string, memoryMB, numInstances uint64, hints map[string]any) api.ExternalSchedulerRequest {
	return api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				ProjectID:        projectID,
				AvailabilityZone: az,
				NumInstances:     numInstances,
				SchedulerHints:   hints,
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
						MemoryMB:   memoryMB,
						VCPUs:      4,
						ExtraSpecs: map[string]string{"hw_version": hwVersion},
					},
				},
			},
		},
		Hosts: []api.ExternalSchedulerHost{
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

	tests := []struct {
		name         string
		objects      []client.Object
		request      api.ExternalSchedulerRequest
		expectAccept bool
	}{
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("50Gi"),
						},
					},
				},
			},
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("90Gi"),
						},
					},
				},
			},
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("50Gi"),
						},
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
			// PAYG headroom = 200 - 50 - 100 = 50 >= 10 → accept
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("50Gi"),
						},
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
			// PAYG headroom = 100 - 50 - 50 = 0 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// No CRs, PAYG headroom = 50 - 0 - 60 = -10 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
		},
		{
			name:         "ACCEPT: no ProjectQuota CRD found (skip enforcement)",
			objects:      []client.Object{},
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
		},
		{
			name:    "SKIP: evacuate intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "evacuate"}),
			expectAccept: true,
		},
		{
			name:    "SKIP: live migration intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "live_migrate"}),
			expectAccept: true,
		},
		{
			name:    "SKIP: reserve_for_failover intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "reserve_for_failover"}),
			expectAccept: true,
		},
		{
			name:    "SKIP: reserve_for_committed_resource intent",
			objects: []client.Object{},
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "reserve_for_committed_resource"}),
			expectAccept: true,
		},
		{
			name:    "SKIP: no hw_version in flavor",
			objects: []client.Object{},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						NumInstances:     1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								MemoryMB:   10240,
								VCPUs:      4,
								ExtraSpecs: map[string]string{},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectAccept: true,
		},
		{
			name:         "SKIP: no project ID",
			objects:      []client.Object{},
			request:      makeQuotaEnforcementRequest("", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
		},
		{
			name:         "SKIP: no availability zone",
			objects:      []client.Object{},
			request:      makeQuotaEnforcementRequest("project-1", "", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("0"),
						},
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
			// Other-project CR has 100Gi free but belongs to project-OTHER.
			// project-1 has no matching CR, PAYG headroom = 5 - 0 - 5 = 0 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("0"),
						},
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
			// CR in az-2 doesn't help. PAYG headroom = 5 - 0 - 5 = 0 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// Planned CR is ignored. PAYG headroom = 5 - 0 - 5 = 0 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// 3 instances * 10240 MB = 30720 MB → ceil(30720/1024) = 30 GiB
			// PAYG headroom = 500 - 0 - 100 = 400 >= 30 → accept
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 3, nil),
			expectAccept: true,
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
			// 3 instances * 10240 MB = 30720 MB → ceil(30720/1024) = 30 GiB
			// PAYG headroom = 120 - 0 - 100 = 20 < 30 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 3, nil),
			expectAccept: false,
		},
		{
			name: "REJECT: resize intent is enforced (not skipped)",
			objects: []client.Object{
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_hana_v2_ram": 10,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_ram": 10},
					},
				},
			},
			// Resize intent IS enforced (not skipped like evacuate/live-migrate).
			// PAYG headroom = 10 - 0 - 10 = 0 < 10 → reject
			request: makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1,
				map[string]any{"_nova_check_type": "resize"}),
			expectAccept: false,
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
			// Cores CR ignored. PAYG headroom = 5 - 0 - 5 = 0 < 10 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// PAYG headroom = 500 - 0 - 100 = 400 >= 10 → accept
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "vmware_v2", 10240, 1, nil),
			expectAccept: true,
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
			// Only RAM quota set. Headroom = 200 - 0 - 50 = 150 >= 10 → accept.
			// Cores/instances not set → not enforced.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
			// Only RAM quota set. Headroom = 55 - 0 - 50 = 5 < 10 → reject.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// Only cores quota set. Headroom = 100 - 10 = 90 >= 4 → accept.
			// RAM/instances not set → not enforced.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
			// Only cores quota set. Headroom = 5 - 3 = 2 < 4 → reject.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
			// Only instances quota set. Headroom = 10 - 5 = 5 >= 1 → accept.
			// RAM/cores not set → not enforced.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
			// Only instances quota set. Headroom = 3 - 3 = 0 < 1 → reject.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: false,
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
						UsedResources: map[string]resource.Quantity{
							"memory": resource.MustParse("50Gi"),
						},
					},
				},
				&v1alpha1.ProjectQuota{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-1-az-1"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-1",
						AvailabilityZone: "az-1",
						Quota: map[string]int64{
							"hw_version_hana_v2_cores": 2,
						},
					},
					Status: v1alpha1.ProjectQuotaStatus{
						PaygUsage: map[string]int64{"hw_version_hana_v2_cores": 2},
					},
				},
			},
			// CR has 50Gi free >= 10 GiB request → CR headroom accept.
			// PAYG cores would reject (2 - 2 = 0 < 4), but CR headroom short-circuits.
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 10240, 1, nil),
			expectAccept: true,
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
			// 2048 MB → ceil(2048/1024) = 2 GiB. PAYG headroom = 1 - 0 - 1 = 0 < 2 → reject
			request:      makeQuotaEnforcementRequest("project-1", "az-1", "hana_v2", 2048, 1, nil),
			expectAccept: false,
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

			traceLog := slog.Default()
			result, err := filter.Run(traceLog, tt.request)
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
		})
	}
}

func TestQuantityToGiB(t *testing.T) {
	tests := []struct {
		name     string
		quantity resource.Quantity
		expected int64
	}{
		{
			name:     "100Gi exact",
			quantity: resource.MustParse("100Gi"),
			expected: 100,
		},
		{
			name:     "1Ti = 1024 GiB",
			quantity: resource.MustParse("1Ti"),
			expected: 1024,
		},
		{
			name:     "512Mi = 1 GiB (ceil)",
			quantity: resource.MustParse("512Mi"),
			expected: 1,
		},
		{
			name:     "1Gi exact",
			quantity: resource.MustParse("1Gi"),
			expected: 1,
		},
		{
			name:     "0 bytes",
			quantity: resource.MustParse("0"),
			expected: 0,
		},
		{
			name:     "1.5Gi = 2 GiB (ceil)",
			quantity: resource.MustParse("1536Mi"),
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := quantityToGiB(tt.quantity)
			if result != tt.expected {
				t.Errorf("quantityToGiB(%v) = %d, want %d", tt.quantity, result, tt.expected)
			}
		})
	}
}

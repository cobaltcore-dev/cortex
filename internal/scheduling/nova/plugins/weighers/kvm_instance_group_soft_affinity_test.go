// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newHypervisorWithInstances(name string, instanceIDs ...string) *hv1.Hypervisor {
	instances := make([]hv1.Instance, len(instanceIDs))
	for i, id := range instanceIDs {
		instances[i] = hv1.Instance{
			ID:     id,
			Name:   "instance-" + id,
			Active: true,
		}
	}
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Instances: instances,
		},
	}
}

func newInstanceGroupRequest(policy string, members, hosts []string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
	}

	spec := api.NovaSpec{
		ProjectID:    "project-A",
		InstanceUUID: "new-instance-uuid",
		NumInstances: 1,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       "m1.large",
				VCPUs:      4,
				MemoryMB:   8192,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	// Only set InstanceGroup if policy is provided
	if policy != "" {
		spec.InstanceGroup = &api.NovaObject[api.NovaInstanceGroup]{
			Data: api.NovaInstanceGroup{
				UUID:    "instance-group-uuid",
				Name:    "test-instance-group",
				Policy:  policy,
				Members: members,
			},
		}
	}

	weights := make(map[string]float64)
	for _, h := range hosts {
		weights[h] = 1.0
	}

	return api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
}

func TestKVMInstanceGroupSoftAffinityStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		hypervisors     []*hv1.Hypervisor
		request         api.ExternalSchedulerRequest
		expectedWeights map[string]float64
		wantErr         bool
	}{
		{
			name: "soft-anti-affinity - hosts with group members get negative weight",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1", "member-2"), // 2 members
				newHypervisorWithInstances("host2", "member-1"),             // 1 member
				newHypervisorWithInstances("host3"),                         // 0 members
			},
			request: newInstanceGroupRequest("soft-anti-affinity", []string{"member-1", "member-2", "member-3"}, []string{"host1", "host2", "host3"}),
			expectedWeights: map[string]float64{
				"host1": -2.0, // 2 members * -1 factor
				"host2": -1.0, // 1 member * -1 factor
				"host3": 0.0,  // 0 members * -1 factor
			},
			wantErr: false,
		},
		{
			name: "soft-affinity - hosts with group members get positive weight",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1", "member-2"), // 2 members
				newHypervisorWithInstances("host2", "member-1"),             // 1 member
				newHypervisorWithInstances("host3"),                         // 0 members
			},
			request: newInstanceGroupRequest("soft-affinity", []string{"member-1", "member-2", "member-3"}, []string{"host1", "host2", "host3"}),
			expectedWeights: map[string]float64{
				"host1": 2.0, // 2 members * 1 factor
				"host2": 1.0, // 1 member * 1 factor
				"host3": 0.0, // 0 members * 1 factor
			},
			wantErr: false,
		},
		{
			name: "no instance group in request - default weights",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "instance-1"),
				newHypervisorWithInstances("host2", "instance-2"),
			},
			request: newInstanceGroupRequest("", nil, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// No instance group, weigher skips, returns default 0
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "affinity policy (not soft) - weigher skips",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1"),
				newHypervisorWithInstances("host2"),
			},
			request: newInstanceGroupRequest("affinity", []string{"member-1"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// affinity policy is not soft-affinity or soft-anti-affinity
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "anti-affinity policy (not soft) - weigher skips",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1"),
				newHypervisorWithInstances("host2"),
			},
			request: newInstanceGroupRequest("anti-affinity", []string{"member-1"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// anti-affinity policy is not soft-anti-affinity
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "empty members list - weigher skips",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "instance-1"),
				newHypervisorWithInstances("host2"),
			},
			request: newInstanceGroupRequest("soft-affinity", []string{}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// Empty members, weigher skips
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "nil members list - weigher skips",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "instance-1"),
				newHypervisorWithInstances("host2"),
			},
			request: newInstanceGroupRequest("soft-affinity", nil, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// Nil members, weigher skips
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "host not found in hypervisor list - skipped",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1"),
				// host2 hypervisor is missing
			},
			request: newInstanceGroupRequest("soft-affinity", []string{"member-1"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				"host1": 1.0, // 1 member found
				"host2": 0,   // missing hypervisor, skipped
			},
			wantErr: false,
		},
		{
			name: "instances not in group members - no weight change",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "other-instance-1", "other-instance-2"),
				newHypervisorWithInstances("host2", "other-instance-3"),
			},
			request: newInstanceGroupRequest("soft-affinity", []string{"member-1", "member-2"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// Instances on hosts are not in the group members list
				"host1": 0.0,
				"host2": 0.0,
			},
			wantErr: false,
		},
		{
			name: "soft-anti-affinity - all hosts have members",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1", "member-2", "member-3"), // 3 members
				newHypervisorWithInstances("host2", "member-4"),                         // 1 member
				newHypervisorWithInstances("host3", "member-5", "member-6"),             // 2 members
			},
			request: newInstanceGroupRequest("soft-anti-affinity",
				[]string{"member-1", "member-2", "member-3", "member-4", "member-5", "member-6"},
				[]string{"host1", "host2", "host3"}),
			expectedWeights: map[string]float64{
				"host1": -3.0, // 3 members * -1 factor
				"host2": -1.0, // 1 member * -1 factor
				"host3": -2.0, // 2 members * -1 factor
			},
			wantErr: false,
		},
		{
			name: "mixed instances - some in group, some not",
			hypervisors: []*hv1.Hypervisor{
				// host1 has 2 group members + 1 non-member
				newHypervisorWithInstances("host1", "member-1", "member-2", "other-instance"),
				// host2 has 1 group member + 2 non-members
				newHypervisorWithInstances("host2", "member-3", "unrelated-1", "unrelated-2"),
			},
			request: newInstanceGroupRequest("soft-affinity",
				[]string{"member-1", "member-2", "member-3"},
				[]string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				"host1": 2.0, // 2 group members (other-instance not counted)
				"host2": 1.0, // 1 group member (unrelated instances not counted)
			},
			wantErr: false,
		},
		{
			name:        "no hypervisors found",
			hypervisors: []*hv1.Hypervisor{},
			request:     newInstanceGroupRequest("soft-affinity", []string{"member-1"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// No hypervisors, hosts are skipped
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "single host with many group members",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "m1", "m2", "m3", "m4", "m5"),
			},
			request: newInstanceGroupRequest("soft-anti-affinity",
				[]string{"m1", "m2", "m3", "m4", "m5"},
				[]string{"host1"}),
			expectedWeights: map[string]float64{
				"host1": -5.0, // 5 members * -1 factor
			},
			wantErr: false,
		},
		{
			name: "unknown policy - weigher skips",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1"),
				newHypervisorWithInstances("host2"),
			},
			request: newInstanceGroupRequest("unknown-policy", []string{"member-1"}, []string{"host1", "host2"}),
			expectedWeights: map[string]float64{
				// Unknown policy, weigher skips
				"host1": 0,
				"host2": 0,
			},
			wantErr: false,
		},
		{
			name: "soft-affinity with single host having all members",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "m1", "m2", "m3"),
				newHypervisorWithInstances("host2"),
				newHypervisorWithInstances("host3"),
			},
			request: newInstanceGroupRequest("soft-affinity",
				[]string{"m1", "m2", "m3"},
				[]string{"host1", "host2", "host3"}),
			expectedWeights: map[string]float64{
				"host1": 3.0, // All 3 members
				"host2": 0.0, // No members
				"host3": 0.0, // No members
			},
			wantErr: false,
		},
		{
			name: "hypervisor with no instances",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithInstances("host1", "member-1"),
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Instances: nil, // nil instances slice
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Instances: []hv1.Instance{}, // empty instances slice
					},
				},
			},
			request: newInstanceGroupRequest("soft-affinity",
				[]string{"member-1"},
				[]string{"host1", "host2", "host3"}),
			expectedWeights: map[string]float64{
				"host1": 1.0, // 1 member
				"host2": 0.0, // nil instances
				"host3": 0.0, // empty instances
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hypervisors))
			for _, hv := range tt.hypervisors {
				objects = append(objects, hv)
			}

			step := &KVMInstanceGroupSoftAffinityStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

			result, err := step.Run(slog.Default(), tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for host, expectedWeight := range tt.expectedWeights {
				actualWeight, ok := result.Activations[host]
				if !ok {
					t.Errorf("expected host %s to be in activations", host)
					continue
				}
				if actualWeight != expectedWeight {
					t.Errorf("for host %s, expected weight %.1f, got %.1f", host, expectedWeight, actualWeight)
				}
			}

			// Verify statistics are populated
			if _, ok := result.Statistics["affinity"]; !ok {
				t.Error("expected statistics to contain 'affinity'")
			}
		})
	}
}

func TestKVMInstanceGroupSoftAffinityStep_IndexRegistration(t *testing.T) {
	factory, ok := Index["kvm_instance_group_soft_affinity"]
	if !ok {
		t.Fatal("kvm_instance_group_soft_affinity not found in Index")
	}

	weigher := factory()
	if weigher == nil {
		t.Fatal("factory returned nil weigher")
	}

	_, ok = weigher.(*KVMInstanceGroupSoftAffinityStep)
	if !ok {
		t.Fatalf("expected *KVMInstanceGroupSoftAffinityStep, got %T", weigher)
	}
}

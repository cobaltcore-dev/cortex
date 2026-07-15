// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newFailoverRes builds a minimal failover Reservation with the given target
// host set on Status.Host and the given allocations map. Resources are omitted
// because the anti-affinity filter does not use them.
func newFailoverRes(name, targetHost string, allocations map[string]string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: v1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
		},
		Status: v1alpha1.ReservationStatus{
			Host: targetHost,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: allocations,
			},
		},
	}
}

func TestFilterInstanceGroupAntiAffinityStep_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-1"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-2"},
					{ID: "vm-uuid-3"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-1"},
					{ID: "vm-uuid-2"},
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Instances: []hv1.Instance{
					{ID: "vm-uuid-4"},
					{ID: "vm-uuid-5"},
					{ID: "vm-uuid-6"},
				},
			},
		},
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		reservations  []client.Object
		opts          FilterInstanceGroupAntiAffinityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No instance group - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID:  "vm-uuid-new",
						InstanceGroup: nil,
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Instance group with affinity policy - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Instance group with soft-anti-affinity policy - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "soft-anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy with empty members list - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - default max_server_per_host=1",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=3",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-4", "vm-uuid-5", "vm-uuid-6"},
								Rules: map[string]any{
									"max_server_per_host": 3,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host5"},
		},
		{
			name: "Anti-affinity policy - host running same VM (resize scenario)",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-1",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - all hosts filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3", "host4"},
		},
		{
			name: "Anti-affinity policy - mixed hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3", "vm-uuid-4", "vm-uuid-5"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3", "host4", "host5"},
		},
		{
			name: "Anti-affinity policy - empty request hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - host with non-member instances",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-100", "vm-uuid-101"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Anti-affinity policy - max_server_per_host=2 with mixed instances",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host5"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "Anti-affinity policy - with instance UUID and project ID",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						ProjectID:    "project-abc",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								UUID:      "ig-uuid-456",
								Policy:    "anti-affinity",
								Members:   []string{"vm-uuid-1"},
								ProjectID: "project-abc",
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - multiple members on same host with max=2",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host3", "host4"},
		},
		{
			name: "Anti-affinity policy - resize with VM on multiple hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-2",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host4"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - single host scenario",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Anti-affinity policy - high max_server_per_host value",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3"},
								Rules: map[string]any{
									"max_server_per_host": 10,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
		// -------------------------------------------------------------------
		// Failover reservation awareness (issue #506)
		// -------------------------------------------------------------------
		{
			name: "Failover placement - flag off - failover slot ignored (backward compat)",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReserveForFailoverIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts:          FilterInstanceGroupAntiAffinityOpts{},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Failover placement - failover flag on - reject when slot present",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReserveForFailoverIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				FailoverPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Failover placement - only vm-flag on - failover slot ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReserveForFailoverIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Regular VM placement - vm-flag on - reject when slot present",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
		},
		{
			name: "Regular VM placement - only failover-flag on - failover slot ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				FailoverPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Failover placement - self-identity - VM has own failover slot on host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-self",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReserveForFailoverIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-self"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-self": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				FailoverPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Mixed running + failover slot at max=1 - reject",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy: "anti-affinity",
								// host2 already runs vm-uuid-1
								Members: []string{"vm-uuid-1", "vm-uuid-group-2"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host2", map[string]string{
					"vm-uuid-group-2": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "max_server_per_host=3 - two members (running+slot) allowed, three rejected",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy: "anti-affinity",
								// host2 runs vm-uuid-1; host3 runs vm-uuid-2 and vm-uuid-3
								Members: []string{"vm-uuid-1", "vm-uuid-2", "vm-uuid-3", "vm-uuid-group-4"},
								Rules: map[string]any{
									"max_server_per_host": 3,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"}, // 1 running + 1 failover slot => 2, allowed (< 3)
					{ComputeHost: "host3"}, // 2 running + 1 failover slot => 3, rejected (>= 3)
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-b2", "host2", map[string]string{
					"vm-uuid-group-4": "sourceHost",
				}),
				newFailoverRes("res-b3", "host3", map[string]string{
					"vm-uuid-group-4": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "Deduplication - same VM in Instances and Allocations counts once",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
								Rules: map[string]any{
									"max_server_per_host": 2,
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			// host2 runs vm-uuid-1, and there is also a failover allocation
			// for vm-uuid-1 on host2 -> should count once, not twice.
			reservations: []client.Object{
				newFailoverRes("res-dup", "host2", map[string]string{
					"vm-uuid-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{},
		},
		{
			name: "Non-failover reservation types are ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "cr-res"},
					Spec: v1alpha1.ReservationSpec{
						Type: v1alpha1.ReservationTypeCommittedResource,
					},
					Status: v1alpha1.ReservationStatus{
						Host: "host1",
						// A CR reservation might carry allocations too, but
						// they must not be interpreted as failover slots.
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
							Allocations: map[string]string{
								"vm-uuid-group-1": "host1",
							},
						},
					},
				},
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "if-res"},
					Spec: v1alpha1.ReservationSpec{
						Type: v1alpha1.ReservationTypeInFlight,
					},
					Status: v1alpha1.ReservationStatus{Host: "host1"},
				},
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		{
			name: "Failover reservation with empty Status.Host - safely ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "res-unplaced"},
					Spec: v1alpha1.ReservationSpec{
						Type: v1alpha1.ReservationTypeFailover,
					},
					Status: v1alpha1.ReservationStatus{
						// Host is empty, no failover allocation.
						FailoverReservation: &v1alpha1.FailoverReservationStatus{
							Allocations: map[string]string{
								"vm-uuid-group-1": "sourceHost",
							},
						},
					},
				},
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
		},
		// -------------------------------------------------------------------
		// Failover reuse intent - anti-affinity must still be enforced so
		// two peers of an anti-affinity group cannot end up sharing the same
		// failover slot at evacuation time.
		// -------------------------------------------------------------------
		{
			name: "Failover reuse - running peer on candidate host is rejected",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReuseFailoverReservationIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
				},
			},
			opts:          FilterInstanceGroupAntiAffinityOpts{},
			expectedHosts: []string{},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Failover reuse - peer's failover slot on candidate rejected with vm-flag on",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: "vm-uuid-new",
						SchedulerHints: map[string]any{
							"_nova_check_type": string(api.ReuseFailoverReservationIntent),
						},
						InstanceGroup: &api.NovaObject[api.NovaInstanceGroup]{
							Data: api.NovaInstanceGroup{
								Policy:  "anti-affinity",
								Members: []string{"vm-uuid-group-1"},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			reservations: []client.Object{
				newFailoverRes("res-a", "host1", map[string]string{
					"vm-uuid-group-1": "sourceHost",
				}),
			},
			opts: FilterInstanceGroupAntiAffinityOpts{
				VMPlacementConsidersFailoverReservation: true,
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterInstanceGroupAntiAffinityStep{}
			step.Options = tt.opts
			objects := make([]client.Object, 0, len(hvs)+len(tt.reservations))
			objects = append(objects, hvs...)
			objects = append(objects, tt.reservations...)
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}

func TestFilterInstanceGroupAntiAffinityStep_SkipsForNonPlacementIntent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}
	// host2 has the group member vm; max_server_per_host=1 → host2 would normally be filtered.
	objects := []client.Object{
		&hv1.Hypervisor{ObjectMeta: v1.ObjectMeta{Name: "host1"}},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{Name: "host2"},
			Status:     hv1.HypervisorStatus{Instances: []hv1.Instance{{ID: "vm-existing"}}},
		},
	}
	step := &FilterInstanceGroupAntiAffinityStep{}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

	for _, intent := range []string{"reserve_for_failover", "reserve_for_committed_resource", "capacity_probe"} {
		t.Run(intent, func(t *testing.T) {
			request := newNovaRequest("vm-new", "proj", "m1.small", "gp", 1, "1Gi", false, []string{"host1", "host2"})
			request.Spec.Data.InstanceGroup = &api.NovaObject[api.NovaInstanceGroup]{
				Data: api.NovaInstanceGroup{
					Policy:  "anti-affinity",
					Members: []string{"vm-existing"},
					Rules:   map[string]any{"max_server_per_host": 1},
				},
			}
			request.Spec.Data.SchedulerHints = map[string]any{"_nova_check_type": intent}

			result, err := step.Run(slog.Default(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Activations) != 2 {
				t.Errorf("expected both hosts to pass, got %d", len(result.Activations))
			}
		})
	}
}

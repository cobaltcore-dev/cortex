// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newCRReservation(name, host, projectID, resourceGroup, az, totalMemory string, allocatedMemory ...string) *v1alpha1.Reservation {
	allocs := make(map[string]v1alpha1.CommittedResourceAllocation)
	for i, mem := range allocatedMemory {
		vmUUID := "vm-" + string(rune('a'+i))
		allocs[vmUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(mem),
			},
		}
	}
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			AvailabilityZone: az,
			TargetHost:       host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(totalMemory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceGroup: resourceGroup,
				Allocations:   allocs,
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.ReservationConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "ReservationActive",
			}},
			Host: host,
		},
	}
}

func newCRRequest(hosts []string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	hints := map[string]any{
		api.HintKeyResourceGroup: []any{"group-2101"},
	}
	spec := api.NovaSpec{
		ProjectID:        "project-A",
		AvailabilityZone: "qa-de-1a",
		SchedulerHints:   hints,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{MemoryMB: 8 * 1024},
		},
	}
	weights := make(map[string]float64, len(hosts))
	for _, h := range hosts {
		weights[h] = 1.0
	}
	return api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestKVMCommittedResourceReservationStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		reservations    []*v1alpha1.Reservation
		request         api.ExternalSchedulerRequest
		opts            KVMCommittedResourceReservationOpts
		expectedWeights map[string]float64
	}{
		{
			name: "host with matching reservation and free capacity gets reservation weight",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1},
		},
		{
			name: "reservation fully allocated leaves no free capacity",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "8Gi", "8Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "reservation partially allocated with enough free capacity",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi", "4Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1},
		},
		{
			name: "wrong project is ignored",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-B", "group-2101", "qa-de-1a", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "wrong resource group is ignored",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-9999", "qa-de-1a", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "wrong AZ is ignored",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1b", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "not-ready reservation is ignored",
			reservations: []*v1alpha1.Reservation{
				func() *v1alpha1.Reservation {
					r := newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi")
					r.Status.Conditions[0].Status = metav1.ConditionFalse
					return r
				}(),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "failover reservation type is ignored",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("res-1", "host1", false, nil),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name:         "no resource group hint skips weigher",
			reservations: []*v1alpha1.Reservation{newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi")},
			request: func() api.ExternalSchedulerRequest {
				r := newCRRequest([]string{"host1", "host2"})
				r.Spec.Data.SchedulerHints = nil
				return r
			}(),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.0, "host2": 0.0},
		},
		{
			name: "nil weights use defaults",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1},
		},
		{
			name:            "no reservations - all hosts get default weight",
			reservations:    []*v1alpha1.Reservation{},
			request:         newCRRequest([]string{"host1", "host2"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "multiple hosts each with their own matching reservation",
			reservations: []*v1alpha1.Reservation{
				newCRReservation("res-1", "host1", "project-A", "group-2101", "qa-de-1a", "16Gi"),
				newCRReservation("res-2", "host2", "project-A", "group-2101", "qa-de-1a", "16Gi"),
			},
			request:         newCRRequest([]string{"host1", "host2", "host3"}),
			opts:            KVMCommittedResourceReservationOpts{ReservationHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 1.0, "host3": 0.1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.reservations))
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

			step := &KVMCommittedResourceReservationStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for host, expectedWeight := range tt.expectedWeights {
				actualWeight := result.Activations[host]
				if actualWeight != expectedWeight {
					t.Errorf("host %s: expected weight %v, got %v", host, expectedWeight, actualWeight)
				}
			}
		})
	}
}

func TestKVMCommittedResourceReservationOpts_Validate(t *testing.T) {
	if err := (KVMCommittedResourceReservationOpts{}).Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

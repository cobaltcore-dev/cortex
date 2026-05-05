// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"log/slog"
	"math"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newFailoverReservationWithGroup creates a failover reservation with a specific resource group.
func newFailoverReservationWithGroup(name, targetHost, resourceGroup string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
				hv1.ResourceMemory: *resource.NewQuantity(8192*1_000_000, resource.DecimalSI),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: resourceGroup,
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			Host: targetHost,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: map[string]string{"some-vm": "some-host"},
			},
		},
	}
}

func newFailoverReservationRequest(resourceGroup string, hosts []string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	spec := api.NovaSpec{
		ProjectID:    "project-A",
		InstanceUUID: "test-instance",
		NumInstances: 1,
		SchedulerHints: map[string]any{
			"_nova_check_type":       string(api.ReserveForFailoverIntent),
			api.HintKeyResourceGroup: resourceGroup,
		},
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:     "m1.large",
				VCPUs:    4,
				MemoryMB: 8192,
				ExtraSpecs: map[string]string{
					"capabilities:hypervisor_type": "qemu",
				},
			},
		},
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

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestKVMFailoverReservationConsolidationStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		reservations    []*v1alpha1.Reservation
		request         api.ExternalSchedulerRequest
		opts            KVMFailoverReservationConsolidationOpts
		expectedWeights map[string]float64
	}{
		{
			name: "consolidation: prefer host with existing failover reservations",
			reservations: []*v1alpha1.Reservation{
				// host1 has 3 reservations (different groups)
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
				newFailoverReservationWithGroup("res-2", "host1", "group-B"),
				newFailoverReservationWithGroup("res-3", "host1", "group-C"),
				// host2 has 1 reservation
				newFailoverReservationWithGroup("res-4", "host2", "group-B"),
			},
			// Request for group-D - no same-group on any host
			request: newFailoverReservationRequest("group-D", []string{"host1", "host2", "host3"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			// T=4, host1: (1/4)*3=0.75, host2: (1/4)*1=0.25, host3: 0
			expectedWeights: map[string]float64{"host1": 0.75, "host2": 0.25, "host3": 0},
		},
		{
			name: "same-group penalty: prefer host with fewer same-group reservations",
			reservations: []*v1alpha1.Reservation{
				// host1 has 5 reservations, 0 same-group (group-A)
				newFailoverReservationWithGroup("res-1", "host1", "group-B"),
				newFailoverReservationWithGroup("res-2", "host1", "group-B"),
				newFailoverReservationWithGroup("res-3", "host1", "group-C"),
				newFailoverReservationWithGroup("res-4", "host1", "group-C"),
				newFailoverReservationWithGroup("res-5", "host1", "group-D"),
				// host2 has 5 reservations, 3 same-group (group-A)
				newFailoverReservationWithGroup("res-6", "host2", "group-A"),
				newFailoverReservationWithGroup("res-7", "host2", "group-A"),
				newFailoverReservationWithGroup("res-8", "host2", "group-A"),
				newFailoverReservationWithGroup("res-9", "host2", "group-C"),
				newFailoverReservationWithGroup("res-10", "host2", "group-D"),
			},
			request: newFailoverReservationRequest("group-A", []string{"host1", "host2", "host3"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			// T=10
			// host1: (1/10)*5 - (0.1/10)*0 = 0.5
			// host2: (1/10)*5 - (0.1/10)*3 = 0.5 - 0.03 = 0.47
			// host3: 0
			expectedWeights: map[string]float64{"host1": 0.5, "host2": 0.47, "host3": 0},
		},
		{
			name: "consolidation dominates: host with reservations preferred over empty host even with same-group",
			reservations: []*v1alpha1.Reservation{
				// host2 has 5 reservations, 3 same-group (group-A)
				newFailoverReservationWithGroup("res-1", "host2", "group-A"),
				newFailoverReservationWithGroup("res-2", "host2", "group-A"),
				newFailoverReservationWithGroup("res-3", "host2", "group-A"),
				newFailoverReservationWithGroup("res-4", "host2", "group-C"),
				newFailoverReservationWithGroup("res-5", "host2", "group-D"),
			},
			request: newFailoverReservationRequest("group-A", []string{"host2", "host3"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			// T=5
			// host2: (1/5)*5 - (0.1/5)*3 = 1.0 - 0.06 = 0.94
			// host3: 0
			expectedWeights: map[string]float64{"host2": 0.94, "host3": 0},
		},
		{
			name:            "no reservations: all hosts get default weight (no effect)",
			reservations:    []*v1alpha1.Reservation{},
			request:         newFailoverReservationRequest("group-A", []string{"host1", "host2"}),
			opts:            KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0, "host2": 0},
		},
		{
			name: "non-failover request: weigher has no effect",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
			},
			// Use a non-failover request (evacuation)
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0, "host2": 0},
		},
		{
			name: "non-failover request without hints: weigher has no effect",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
			},
			// Use a non-failover request (no hints = create intent)
			request:         newNovaRequest("instance-123", false, []string{"host1", "host2"}),
			opts:            KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0, "host2": 0},
		},
		{
			name: "default options work correctly",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-B"),
				newFailoverReservationWithGroup("res-2", "host1", "group-A"), // same group
				newFailoverReservationWithGroup("res-3", "host2", "group-B"),
			},
			request: newFailoverReservationRequest("group-A", []string{"host1", "host2", "host3"}),
			opts:    KVMFailoverReservationConsolidationOpts{}, // nil = use defaults
			// Defaults: TotalCountWeight=1.0, SameSpecPenalty=0.1, T=3
			// host1: (1/3)*2 - (0.1/3)*1 ≈ 0.6667 - 0.0333 = 0.6333
			// host2: (1/3)*1 - (0.1/3)*0 ≈ 0.3333
			// host3: 0
			expectedWeights: map[string]float64{"host1": 2.0/3.0 - 0.1/3.0, "host2": 1.0 / 3.0, "host3": 0},
		},
		{
			name: "committed resource reservations are ignored",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
				newCommittedReservation("committed-1", "host2"),
			},
			request: newFailoverReservationRequest("group-A", []string{"host1", "host2", "host3"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			// T=1 (only 1 failover reservation), committed reservation ignored
			// host1: (1/1)*1 - (0.1/1)*1 = 0.9
			// host2: 0 (committed reservation not counted)
			// host3: 0
			expectedWeights: map[string]float64{"host1": 0.9, "host2": 0, "host3": 0},
		},
		{
			name: "failed reservations are ignored",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
				newFailoverReservation("failed-res", "host2", true, map[string]string{"vm-1": "h-1"}),
			},
			request: newFailoverReservationRequest("group-A", []string{"host1", "host2"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(1.0), SameSpecPenalty: testlib.Ptr(0.1)},
			// T=1 (failed reservation ignored)
			// host1: (1/1)*1 - (0.1/1)*1 = 0.9
			// host2: 0
			expectedWeights: map[string]float64{"host1": 0.9, "host2": 0},
		},
		{
			name: "custom weights adjust scoring",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservationWithGroup("res-1", "host1", "group-A"),
				newFailoverReservationWithGroup("res-2", "host1", "group-A"),
				newFailoverReservationWithGroup("res-3", "host2", "group-B"),
			},
			request: newFailoverReservationRequest("group-A", []string{"host1", "host2"}),
			opts:    KVMFailoverReservationConsolidationOpts{TotalCountWeight: testlib.Ptr(2.0), SameSpecPenalty: testlib.Ptr(0.5)},
			// T=3, W=2.0, P=0.5
			// host1: (2/3)*2 - (0.5/3)*2 = 1.3333 - 0.3333 = 1.0
			// host2: (2/3)*1 - (0.5/3)*0 = 0.6667
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 2.0 / 3.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.reservations))
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

			step := &KVMFailoverReservationConsolidationStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request, lib.Options{})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for host, expectedWeight := range tt.expectedWeights {
				actualWeight, ok := result.Activations[host]
				if !ok {
					t.Errorf("expected host %s to be in activations", host)
					continue
				}
				if !approxEqual(actualWeight, expectedWeight, 0.0001) {
					t.Errorf("host %s: expected weight %v, got %v", host, expectedWeight, actualWeight)
				}
			}
		})
	}
}

func TestKVMFailoverReservationConsolidationOpts_Defaults(t *testing.T) {
	opts := KVMFailoverReservationConsolidationOpts{}
	if opts.GetTotalCountWeight() != 1.0 {
		t.Errorf("expected default TotalCountWeight 1.0, got %v", opts.GetTotalCountWeight())
	}
	if opts.GetSameSpecPenalty() != 0.1 {
		t.Errorf("expected default SameSpecPenalty 0.1, got %v", opts.GetSameSpecPenalty())
	}
}

func TestKVMFailoverReservationConsolidationOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    KVMFailoverReservationConsolidationOpts
		wantErr string
	}{
		{
			name: "valid: both set, p < w",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(2.0),
				SameSpecPenalty:  testlib.Ptr(0.5),
			},
		},
		{
			name: "valid: defaults (nil)",
			opts: KVMFailoverReservationConsolidationOpts{},
		},
		{
			name: "valid: both zero",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(0.0),
				SameSpecPenalty:  testlib.Ptr(0.0),
			},
		},
		{
			name: "invalid: negative totalCountWeight",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(-1.0),
			},
			wantErr: "totalCountWeight must be non-negative",
		},
		{
			name: "invalid: negative sameSpecPenalty",
			opts: KVMFailoverReservationConsolidationOpts{
				SameSpecPenalty: testlib.Ptr(-0.1),
			},
			wantErr: "sameSpecPenalty must be non-negative",
		},
		{
			name: "invalid: p >= w",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(1.0),
				SameSpecPenalty:  testlib.Ptr(1.0),
			},
			wantErr: "sameSpecPenalty must be less than totalCountWeight",
		},
		{
			name: "invalid: w=0 with p>0 (default penalty with zero weight)",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(0.0),
				// SameSpecPenalty defaults to 0.1
			},
			wantErr: "sameSpecPenalty must be zero when totalCountWeight is zero",
		},
		{
			name: "invalid: w=0 with explicit p>0",
			opts: KVMFailoverReservationConsolidationOpts{
				TotalCountWeight: testlib.Ptr(0.0),
				SameSpecPenalty:  testlib.Ptr(0.5),
			},
			wantErr: "sameSpecPenalty must be zero when totalCountWeight is zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error %q, got nil", tt.wantErr)
				} else if err.Error() != tt.wantErr {
					t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}

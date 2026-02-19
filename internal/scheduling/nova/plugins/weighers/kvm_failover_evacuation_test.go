// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Test Helpers
// ============================================================================

func buildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

func newFailoverReservation(name, targetHost string, failed bool, allocations map[string]string) *v1alpha1.Reservation {
	conditionStatus := metav1.ConditionTrue
	conditionReason := "ReservationActive"
	if failed {
		conditionStatus = metav1.ConditionFalse
		conditionReason = "ReservationFailed"
	}

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("4"),
				"memory": resource.MustParse("8Gi"),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: "m1.large",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: conditionStatus,
					Reason: conditionReason,
				},
			},
			Host: targetHost,
		},
	}
	if allocations != nil {
		res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
			Allocations: allocations,
		}
	}
	return res
}

func newCommittedReservation(name, targetHost string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("4"),
				"memory": resource.MustParse("8Gi"),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "project-A",
				ResourceName: "m1.large",
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
		},
	}
}

// parseMemoryToMB converts a memory string (e.g., "8Gi", "4096Mi") to megabytes.
func parseMemoryToMB(memory string) uint64 {
	q := resource.MustParse(memory)
	bytes := q.Value()
	return uint64(bytes / (1024 * 1024)) //nolint:gosec // test code
}

func newNovaRequest(instanceUUID string, evacuation bool, hosts []string) api.ExternalSchedulerRequest { //nolint:unparam // instanceUUID varies in real usage
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
	}

	var schedulerHints map[string]any
	if evacuation {
		schedulerHints = map[string]any{
			"_nova_check_type": []any{"evacuate"},
		}
	}

	spec := api.NovaSpec{
		ProjectID:      "project-A",
		InstanceUUID:   instanceUUID,
		NumInstances:   1,
		SchedulerHints: schedulerHints,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       "m1.large",
				VCPUs:      4,
				MemoryMB:   parseMemoryToMB("8Gi"),
				ExtraSpecs: extraSpecs,
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

// ============================================================================
// Tests
// ============================================================================

func TestKVMFailoverEvacuationStep_Run(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name            string
		reservations    []*v1alpha1.Reservation
		request         api.ExternalSchedulerRequest
		opts            KVMFailoverEvacuationOpts
		expectedWeights map[string]float64
	}{
		{
			name: "VM in failover reservation gets high weight on reserved host",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2", "host3"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1, "host3": 0.1},
		},
		{
			name: "VM not in any failover reservation gets default weight on all hosts",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"other-instance": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2", "host3"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1, "host3": 0.1},
		},
		{
			name: "VM in multiple failover reservations gets high weight on all reserved hosts",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"instance-123": "original-host"}),
				newFailoverReservation("failover-2", "host3", false, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2", "host3"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1, "host3": 1.0},
		},
		{
			name:            "No reservations - all hosts get default weight",
			reservations:    []*v1alpha1.Reservation{},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "Custom weights are applied correctly",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(0.9), DefaultHostWeight: testlib.Ptr(0.05)},
			expectedWeights: map[string]float64{"host1": 0.9, "host2": 0.05},
		},
		{
			name: "Nil weights use defaults (FailoverHostWeight=1.0, DefaultHostWeight=0.1)",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: nil, DefaultHostWeight: nil},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1},
		},
		{
			name: "Failed reservation is ignored",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failed-failover", "host1", true, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "CommittedResource reservation is ignored",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("committed-res", "host1"),
			},
			request:         newNovaRequest("instance-123", true, []string{"host1", "host2"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "Non-evacuation request skips failover weighing",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", false, map[string]string{"instance-123": "original-host"}),
			},
			request:         newNovaRequest("instance-123", false, []string{"host1", "host2", "host3"}),
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: testlib.Ptr(1.0), DefaultHostWeight: testlib.Ptr(0.1)},
			expectedWeights: map[string]float64{"host1": 0, "host2": 0, "host3": 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.reservations))
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

			step := &KVMFailoverEvacuationStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
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
					t.Errorf("host %s: expected weight %v, got %v", host, expectedWeight, actualWeight)
				}
			}
		})
	}
}

func TestKVMFailoverEvacuationOpts_Validate(t *testing.T) {
	opts := KVMFailoverEvacuationOpts{
		FailoverHostWeight: testlib.Ptr(1.0),
		DefaultHostWeight:  testlib.Ptr(0.1),
	}
	if err := opts.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

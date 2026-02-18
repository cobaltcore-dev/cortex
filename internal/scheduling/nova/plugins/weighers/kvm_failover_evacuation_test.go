// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	th "github.com/cobaltcore-dev/cortex/internal/scheduling/nova/testhelpers"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKVMFailoverEvacuationStep_Run(t *testing.T) {
	scheme := th.BuildTestScheme(t)

	tests := []struct {
		name            string
		reservations    []th.ReservationArgs
		request         th.NovaRequestArgs
		opts            KVMFailoverEvacuationOpts
		expectedWeights map[string]float64
	}{
		{
			name: "VM in failover reservation gets high weight on reserved host",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2", "host3"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1, "host3": 0.1},
		},
		{
			name: "VM not in any failover reservation gets default weight on all hosts",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"other-instance": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2", "host3"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1, "host3": 0.1},
		},
		{
			name: "VM in multiple failover reservations gets high weight on all reserved hosts",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
				{Name: "failover-2", TargetHost: "host3", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2", "host3"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1, "host3": 1.0},
		},
		{
			name:            "No reservations - all hosts get default weight",
			reservations:    []th.ReservationArgs{},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "Custom weights are applied correctly",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 0.9, DefaultHostWeight: 0.05},
			expectedWeights: map[string]float64{"host1": 0.9, "host2": 0.05},
		},
		{
			name: "Default weights used when opts are zero",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2"}},
			opts:            KVMFailoverEvacuationOpts{},
			expectedWeights: map[string]float64{"host1": 1.0, "host2": 0.1},
		},
		{
			name: "Failed reservation is ignored",
			reservations: []th.ReservationArgs{
				{Name: "failed-failover", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Failed: true, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "CommittedResource reservation is ignored",
			reservations: []th.ReservationArgs{
				{Name: "committed-res", TargetHost: "host1"},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: true, Hosts: []string{"host1", "host2"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 0.1, "host2": 0.1},
		},
		{
			name: "Non-evacuation request skips failover weighing",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "original-host"}},
			},
			request:         th.NovaRequestArgs{InstanceUUID: "instance-123", Evacuation: false, Hosts: []string{"host1", "host2", "host3"}},
			opts:            KVMFailoverEvacuationOpts{FailoverHostWeight: 1.0, DefaultHostWeight: 0.1},
			expectedWeights: map[string]float64{"host1": 0, "host2": 0, "host3": 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.reservations))
			for _, r := range th.NewReservations(tt.reservations) {
				objects = append(objects, r)
			}

			step := &KVMFailoverEvacuationStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), th.NewNovaRequest(tt.request))
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
		FailoverHostWeight: 1.0,
		DefaultHostWeight:  0.1,
	}
	if err := opts.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

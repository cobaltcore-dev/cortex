// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newCRMigrationSlotFilter builds a FilterCRMigrationSlotStep backed by a fake client
// seeded with the given objects.
func newCRMigrationSlotFilter(t *testing.T, objs ...client.Object) *FilterCRMigrationSlotStep {
	t.Helper()
	scheme := buildTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &FilterCRMigrationSlotStep{
		BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]{
			BaseFilterWeigherPipelineStep: lib.BaseFilterWeigherPipelineStep[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]{
				Client: c,
			},
		},
	}
}

// liveMigrateRequest builds a minimal live-migration request for "vm-migrating".
func liveMigrateRequest(hosts ...string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	return api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID: "vm-migrating",
				ProjectID:    "proj-1",
				SchedulerHints: map[string]any{
					"_nova_check_type": "live_migrate",
				},
			},
		},
		Hosts: hostList,
	}
}

// sourceSlotFor builds a ready 16Gi CR reservation slot on host-src with instanceUUID confirmed.
func sourceSlotFor(instanceUUID string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slot-src",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-src",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("16Gi"),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "proj-1",
				ResourceGroup: "hana-v2",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-src",
			Conditions: []metav1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: metav1.ConditionTrue, Reason: "ReservationActive"},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]string{instanceUUID: "host-src"},
			},
		},
	}
}

// emptyReservation builds a ready CR reservation slot with no VM allocations.
func emptyReservation(name, host, resourceGroup, slotMemory string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(slotMemory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "proj-1",
				ResourceGroup: resourceGroup,
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			Conditions: []metav1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: metav1.ConditionTrue, Reason: "ReservationActive"},
			},
		},
	}
}

// hvWithFreeMemory builds a Hypervisor with 32Gi effective capacity and zero allocation.
func hvWithFreeMemory(name string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("32Gi"),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
}

func TestFilterCRMigrationSlot(t *testing.T) {
	const instanceUUID = "vm-migrating"

	// zeroMemorySourceSlot is a source slot with no memory resource — used to test
	// the zero-slot-memory guard.
	zeroMemorySourceSlot := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slot-src",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-src",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "proj-1",
				ResourceGroup: "hana-v2",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-src",
			Conditions: []metav1.Condition{
				{Type: v1alpha1.ReservationConditionReady, Status: metav1.ConditionTrue, Reason: "ReservationActive"},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]string{instanceUUID: "host-src"},
			},
		},
	}

	tests := []struct {
		name          string
		objects       []client.Object
		request       api.ExternalSchedulerRequest
		wantHosts     []string // hosts that must appear in Activations
		wantFiltered  []string // hosts that must NOT appear in Activations
		wantHostCount int      // total expected Activations size
	}{
		{
			name:    "non-migration intent: all hosts pass through unchanged",
			objects: nil,
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: instanceUUID,
						ProjectID:    "proj-1",
						// no _nova_check_type → CreateIntent
					},
				},
				Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host-1"}, {ComputeHost: "host-2"}},
			},
			wantHosts:     []string{"host-1", "host-2"},
			wantHostCount: 2,
		},
		{
			name:          "no source slot: all candidates pass through (fallback)",
			objects:       []client.Object{},
			request:       liveMigrateRequest("host-1", "host-2"),
			wantHosts:     []string{"host-1", "host-2"},
			wantHostCount: 2,
		},
		{
			name: "slot size filtering: only host with matching 16Gi slot passes",
			objects: []client.Object{
				sourceSlotFor(instanceUUID),
				emptyReservation("slot-a", "host-a", "hana-v2", "16Gi"),
				emptyReservation("slot-b", "host-b", "hana-v2", "8Gi"),
				hvWithFreeMemory("host-src"),
				hvWithFreeMemory("host-a"),
				hvWithFreeMemory("host-b"),
				hvWithFreeMemory("host-c"),
			},
			request:       liveMigrateRequest("host-a", "host-b", "host-c"),
			wantHosts:     []string{"host-a"},
			wantFiltered:  []string{"host-b", "host-c"},
			wantHostCount: 1,
		},
		{
			name: "no slot on any candidate: fallback returns all candidates",
			objects: []client.Object{
				sourceSlotFor(instanceUUID),
				hvWithFreeMemory("host-src"),
				hvWithFreeMemory("host-a"),
				hvWithFreeMemory("host-b"),
			},
			request:       liveMigrateRequest("host-a", "host-b"),
			wantHosts:     []string{"host-a", "host-b"},
			wantHostCount: 2,
		},
		{
			name: "wrong resource group on target: slot does not match, fallback",
			objects: []client.Object{
				sourceSlotFor(instanceUUID),
				emptyReservation("slot-a", "host-a", "general-v3", "16Gi"),
				hvWithFreeMemory("host-src"),
				hvWithFreeMemory("host-a"),
			},
			request:       liveMigrateRequest("host-a"),
			wantHosts:     []string{"host-a"},
			wantHostCount: 1,
		},
		{
			name: "source slot has zero memory: filter skips slot check, all candidates pass",
			objects: []client.Object{
				zeroMemorySourceSlot,
				hvWithFreeMemory("host-a"),
			},
			request:       liveMigrateRequest("host-a"),
			wantHosts:     []string{"host-a"},
			wantHostCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := newCRMigrationSlotFilter(t, tt.objects...)
			result, err := filter.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, host := range tt.wantHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %q in activations", host)
				}
			}
			for _, host := range tt.wantFiltered {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %q to be filtered out", host)
				}
			}
			if len(result.Activations) != tt.wantHostCount {
				t.Errorf("expected %d hosts, got %d: %v", tt.wantHostCount, len(result.Activations), result.Activations)
			}
		})
	}
}

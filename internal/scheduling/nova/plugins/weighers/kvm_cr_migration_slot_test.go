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

// newCRMigrationSlotWeigher builds a KVMCRMigrationSlotStep backed by a fake client.
func newCRMigrationSlotWeigher(t *testing.T, opts KVMCRMigrationSlotOpts, objs ...client.Object) *KVMCRMigrationSlotStep {
	t.Helper()
	scheme := buildTestScheme(t)
	step := &KVMCRMigrationSlotStep{}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	step.Options = opts
	return step
}

// migrationRequest builds a live-migration request for instanceUUID from the given candidate hosts.
func migrationRequest(instanceUUID, projectID string, hosts ...string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	return api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID: instanceUUID,
				ProjectID:    projectID,
				SchedulerHints: map[string]any{
					"_nova_check_type": "live_migrate",
				},
			},
		},
		Hosts: hostList,
	}
}

// confirmedSourceSlot builds a ready CR reservation with instanceUUID confirmed in Status.
func confirmedSourceSlot(instanceUUID, host, resourceGroup, memory string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slot-src-" + host,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(memory),
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
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: map[string]string{instanceUUID: host},
			},
		},
	}
}

// emptyTargetSlot builds a ready CR reservation with no VM allocations on the given host.
func emptyTargetSlot(name, host, resourceGroup, memory string) *v1alpha1.Reservation {
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
				hv1.ResourceMemory: resource.MustParse(memory),
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

// hvWithFreeMemory builds a Hypervisor with the given effective capacity and zero allocation.
func hvWithFreeMemory(name, memory string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(memory),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
}

func TestKVMCRMigrationSlotStep_Run(t *testing.T) {
	const (
		instanceUUID = "vm-migrating"
		projectID    = "proj-1"
	)

	defaultOpts := KVMCRMigrationSlotOpts{SlotHostWeight: floatPtr(1.0), DefaultHostWeight: floatPtr(0.1)}

	tests := []struct {
		name            string
		objects         []client.Object
		request         api.ExternalSchedulerRequest
		opts            KVMCRMigrationSlotOpts
		expectedWeights map[string]float64
	}{
		{
			name: "non-migration intent: all hosts get no-effect weight",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: instanceUUID,
						ProjectID:    projectID,
						// no _nova_check_type → CreateIntent
					},
				},
				Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host-a"}, {ComputeHost: "host-b"}},
			},
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 0.0, "host-b": 0.0},
		},
		{
			name:            "no source slot for VM: all candidates get no-effect weight",
			objects:         []client.Object{},
			request:         migrationRequest(instanceUUID, projectID, "host-a", "host-b"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 0.0, "host-b": 0.0},
		},
		{
			name: "host with matching slot gets slot weight, others get default weight",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
				emptyTargetSlot("slot-a", "host-a", "hana-v2", "16Gi"),
				emptyTargetSlot("slot-b", "host-b", "hana-v2", "8Gi"), // too small
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a", "host-b", "host-c"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 1.0, "host-b": 0.1, "host-c": 0.1},
		},
		{
			name: "no compatible slot on any candidate: hosts penalised (no capacity)",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a", "host-b"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 0.1, "host-b": 0.1},
		},
		{
			name: "host with free capacity but no slot: boosted via accommodate path",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
				hvWithFreeMemory("host-a", "32Gi"), // enough free memory for the slot
				hvWithFreeMemory("host-b", "8Gi"),  // too small for the slot
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a", "host-b"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 1.0, "host-b": 0.1},
		},
		{
			name: "wrong resource group on target: no slot match, falls back to capacity check",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
				emptyTargetSlot("slot-a", "host-a", "general-v3", "16Gi"),
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 0.1},
		},
		{
			name: "source slot has zero memory: no-effect weight for all",
			objects: []client.Object{
				// source slot with no memory resource
				func() *v1alpha1.Reservation {
					res := confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "0")
					res.Spec.Resources = map[hv1.ResourceName]resource.Quantity{} // no memory key
					return res
				}(),
				emptyTargetSlot("slot-a", "host-a", "hana-v2", "16Gi"),
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a"),
			opts:            defaultOpts,
			expectedWeights: map[string]float64{"host-a": 0.0},
		},
		{
			name: "nil opts use default weights",
			objects: []client.Object{
				confirmedSourceSlot(instanceUUID, "host-src", "hana-v2", "16Gi"),
				emptyTargetSlot("slot-a", "host-a", "hana-v2", "16Gi"),
			},
			request:         migrationRequest(instanceUUID, projectID, "host-a", "host-b"),
			opts:            KVMCRMigrationSlotOpts{}, // nil → defaults: slot=0.1, default=0.0
			expectedWeights: map[string]float64{"host-a": 0.1, "host-b": 0.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weigher := newCRMigrationSlotWeigher(t, tt.opts, tt.objects...)
			result, err := weigher.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for host, expected := range tt.expectedWeights {
				actual := result.Activations[host]
				if actual != expected {
					t.Errorf("host %q: expected weight %v, got %v", host, expected, actual)
				}
			}
			if len(result.Activations) != len(tt.expectedWeights) {
				t.Errorf("expected %d hosts in activations, got %d: %v",
					len(tt.expectedWeights), len(result.Activations), result.Activations)
			}
		})
	}
}

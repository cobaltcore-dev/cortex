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

// liveMigrateRequest builds a minimal live-migration request for instanceUUID/projectID.
func liveMigrateRequest(instanceUUID, projectID string, hosts ...string) api.ExternalSchedulerRequest {
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

// confirmedReservation builds a ready CR reservation slot with the VM UUID confirmed in
// Status.Allocations, used to simulate a VM that is currently running on that slot.
func confirmedReservation(name, host, projectID, resourceGroup, slotMemory, vmMemory, instanceUUID string) *v1alpha1.Reservation {
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
				ProjectID:     projectID,
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

// emptyReservation builds a ready CR reservation slot with no VM allocations.
func emptyReservation(name, host, projectID, resourceGroup, slotMemory string) *v1alpha1.Reservation {
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
				ProjectID:     projectID,
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

func TestFilterKVMCRMigrationSlot_NonMigrationPassthrough(t *testing.T) {
	filter := newCRMigrationSlotFilter(t)
	req := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID: "vm-1",
				ProjectID:    "proj-1",
				// no _nova_check_type → CreateIntent
			},
		},
		Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host-1"}, {ComputeHost: "host-2"}},
	}
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected 2 hosts to pass through, got %d", len(result.Activations))
	}
}

func TestFilterKVMCRMigrationSlot_NoSourceSlot_Passthrough(t *testing.T) {
	// VM has no CR reservation — should pass all candidates through unchanged.
	filter := newCRMigrationSlotFilter(t)
	req := liveMigrateRequest("vm-no-slot", "proj-1", "host-1", "host-2")

	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected 2 candidates (passthrough), got %d", len(result.Activations))
	}
}

func TestFilterKVMCRMigrationSlot_SlotSizeFiltering(t *testing.T) {
	// VM is confirmed on source slot (16Gi).
	// host-a has an empty 16Gi slot → should pass.
	// host-b has only an 8Gi slot → should be filtered out.
	// host-c has no reservation at all → should be filtered out.
	instanceUUID := "vm-migrating"
	projectID := "proj-1"
	resourceGroup := "hana-v2"

	srcSlot := confirmedReservation("slot-src", "host-src", projectID, resourceGroup, "16Gi", "8Gi", instanceUUID)
	slotA := emptyReservation("slot-a", "host-a", projectID, resourceGroup, "16Gi")
	slotB := emptyReservation("slot-b", "host-b", projectID, resourceGroup, "8Gi")

	filter := newCRMigrationSlotFilter(t,
		srcSlot, slotA, slotB,
		hvWithFreeMemory("host-src", "32Gi"),
		hvWithFreeMemory("host-a", "32Gi"),
		hvWithFreeMemory("host-b", "32Gi"),
		hvWithFreeMemory("host-c", "32Gi"),
	)

	req := liveMigrateRequest(instanceUUID, projectID, "host-a", "host-b", "host-c")
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := result.Activations["host-a"]; !ok {
		t.Error("expected host-a (16Gi slot) to pass")
	}
	if _, ok := result.Activations["host-b"]; ok {
		t.Error("expected host-b (8Gi slot, too small) to be filtered out")
	}
	if _, ok := result.Activations["host-c"]; ok {
		t.Error("expected host-c (no slot) to be filtered out")
	}
	if len(result.Activations) != 1 {
		t.Errorf("expected 1 passing host, got %d", len(result.Activations))
	}
}

func TestFilterKVMCRMigrationSlot_Fallback_NoSlotOnAnyCandidate(t *testing.T) {
	// No candidate has a matching slot → all candidates must be returned (fallback).
	instanceUUID := "vm-migrating"
	projectID := "proj-1"
	resourceGroup := "hana-v2"

	srcSlot := confirmedReservation("slot-src", "host-src", projectID, resourceGroup, "16Gi", "8Gi", instanceUUID)

	filter := newCRMigrationSlotFilter(t,
		srcSlot,
		hvWithFreeMemory("host-src", "32Gi"),
		hvWithFreeMemory("host-a", "32Gi"),
		hvWithFreeMemory("host-b", "32Gi"),
	)

	req := liveMigrateRequest(instanceUUID, projectID, "host-a", "host-b")
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 2 {
		t.Errorf("expected fallback to return all 2 candidates, got %d", len(result.Activations))
	}
}

func TestFilterKVMCRMigrationSlot_WrongProjectFiltered(t *testing.T) {
	// Target host has a slot but for a different project → should not count.
	instanceUUID := "vm-migrating"
	projectID := "proj-1"
	resourceGroup := "hana-v2"

	srcSlot := confirmedReservation("slot-src", "host-src", projectID, resourceGroup, "16Gi", "8Gi", instanceUUID)
	slotWrongProject := emptyReservation("slot-a", "host-a", "proj-OTHER", resourceGroup, "16Gi")

	filter := newCRMigrationSlotFilter(t,
		srcSlot, slotWrongProject,
		hvWithFreeMemory("host-src", "32Gi"),
		hvWithFreeMemory("host-a", "32Gi"),
	)

	req := liveMigrateRequest(instanceUUID, projectID, "host-a")
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No matching slot → fallback → host-a still returned.
	if len(result.Activations) != 1 {
		t.Errorf("expected fallback with 1 candidate, got %d", len(result.Activations))
	}
}

func TestFilterKVMCRMigrationSlot_WrongResourceGroupFiltered(t *testing.T) {
	instanceUUID := "vm-migrating"
	projectID := "proj-1"

	srcSlot := confirmedReservation("slot-src", "host-src", projectID, "hana-v2", "16Gi", "8Gi", instanceUUID)
	slotWrongGroup := emptyReservation("slot-a", "host-a", projectID, "general-v3", "16Gi")

	filter := newCRMigrationSlotFilter(t,
		srcSlot, slotWrongGroup,
		hvWithFreeMemory("host-src", "32Gi"),
		hvWithFreeMemory("host-a", "32Gi"),
	)

	req := liveMigrateRequest(instanceUUID, projectID, "host-a")
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No matching slot → fallback.
	if len(result.Activations) != 1 {
		t.Errorf("expected fallback with 1 candidate, got %d", len(result.Activations))
	}
}

func TestFilterCRMigrationSlot_ZeroSlotMemory_Passthrough(t *testing.T) {
	// Source slot has no memory resource entry → filter must pass all candidates through.
	instanceUUID := "vm-migrating"
	projectID := "proj-1"

	// Build a reservation with the VM confirmed but Spec.Resources deliberately empty.
	srcSlot := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slot-src",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-src",
			// No Resources entry → memory quantity is zero.
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
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

	filter := newCRMigrationSlotFilter(t, srcSlot, hvWithFreeMemory("host-a", "32Gi"))
	req := liveMigrateRequest(instanceUUID, projectID, "host-a")
	result, err := filter.Run(slog.Default(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 1 {
		t.Errorf("expected passthrough with 1 candidate, got %d", len(result.Activations))
	}
}

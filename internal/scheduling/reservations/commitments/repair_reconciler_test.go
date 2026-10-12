// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// ============================================================================
// Test helpers shared by repair tests
// ============================================================================

func repairTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1: %v", err)
	}
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("add hv1: %v", err)
	}
	return s
}

// newRepairTestClient builds a fake client with the field indexes required by RepairReconciler.
func newRepairTestClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(repairTestScheme(t)).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.CommittedResource{}, &v1alpha1.Reservation{}).
		WithIndex(&v1alpha1.CommittedResource{}, idxCommittedResourceByProjectID, func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.ProjectID == "" {
				return nil
			}
			return []string{cr.Spec.ProjectID}
		}).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByCommitmentUUID, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.CommitmentUUID == "" {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		}).
		Build()
}

// newRepairFlavorKnowledge returns a Knowledge CRD containing the test flavor group.
func newRepairFlavorKnowledge(t *testing.T) *v1alpha1.Knowledge {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"features": []map[string]any{
			{
				"name": "test-group",
				"flavors": []map[string]any{
					{"name": "large", "memoryMB": 32768, "vcpus": 16, "extraSpecs": map[string]string{}},
					{"name": "medium", "memoryMB": 16384, "vcpus": 8, "extraSpecs": map[string]string{}},
					{"name": "small", "memoryMB": 8192, "vcpus": 4, "extraSpecs": map[string]string{}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal knowledge: %v", err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw:       runtime.RawExtension{Raw: raw},
			RawLength: 1,
			Conditions: []metav1.Condition{
				{Type: v1alpha1.KnowledgeConditionReady, Status: metav1.ConditionTrue, Reason: "Ready"},
			},
		},
	}
}

// newRepairCR returns an active memory CommittedResource for project-1 / test-group / az-1.
func newRepairCR(name, commitmentUUID string) *v1alpha1.CommittedResource {
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   commitmentUUID,
			FlavorGroupName:  "test-group",
			ProjectID:        "project-1",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			AvailabilityZone: "az-1",
			State:            v1alpha1.CommitmentStatusConfirmed,
		},
	}
}

// newRepairSlot returns a CR Reservation slot for a given commitment, host, and memory size.
func newRepairSlot(name, commitmentUUID, targetHost string, memGiB int64, allocs map[string]v1alpha1.CommittedResourceAllocation) *v1alpha1.Reservation { //nolint:unparam
	if allocs == nil {
		allocs = map[string]v1alpha1.CommittedResourceAllocation{}
	}
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(memGiB*1024*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: commitmentUUID,
				ProjectID:      "project-1",
				ResourceGroup:  "test-group",
				Allocations:    allocs,
			},
		},
	}
}

// repairHV builds an HV in az-1 with the given active instance IDs.
func repairHV(name string, activeIDs ...string) *hv1.Hypervisor {
	instances := make([]hv1.Instance, len(activeIDs))
	for i, id := range activeIDs {
		instances[i] = hv1.Instance{ID: id, Name: id, Active: true}
	}
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"topology.kubernetes.io/zone": "az-1"},
		},
		Status: hv1.HypervisorStatus{Instances: instances},
	}
}

// repairVM builds a VM with both memory and vcpus resources.
func repairVM(uuid, hvName, project, flavor string, memMB, vcpus uint64) reservations.VM {
	return reservations.VM{
		UUID:              uuid,
		ProjectID:         project,
		FlavorName:        flavor,
		CurrentHypervisor: hvName,
		Resources: map[string]resource.Quantity{
			"memory": *resource.NewQuantity(int64(memMB)*1024*1024, resource.BinarySI), //nolint:gosec
			"vcpus":  *resource.NewQuantity(int64(vcpus), resource.DecimalSI),          //nolint:gosec
		},
	}
}

// slotAllocs retrieves the Spec.Allocations of a named Reservation from the fake client.
func slotAllocs(t *testing.T, k8sClient client.Client, name string) map[string]v1alpha1.CommittedResourceAllocation {
	t.Helper()
	var res v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &res); err != nil {
		t.Fatalf("get reservation %s: %v", name, err)
	}
	if res.Spec.CommittedResourceReservation == nil {
		return nil
	}
	return res.Spec.CommittedResourceReservation.Allocations
}

// slotTargetHost returns the TargetHost of a named Reservation from the fake client.
func slotTargetHost(t *testing.T, k8sClient client.Client, name string) string {
	t.Helper()
	var res v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &res); err != nil {
		t.Fatalf("get reservation %s: %v", name, err)
	}
	return res.Spec.TargetHost
}

// repairReconcilerReq returns a reconcile request for the named CommittedResource.
func repairReconcilerReq(name string) ctrl.Request { //nolint:unparam
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// newTestRepairReconciler builds a RepairReconciler with a 1-hour cooldown and an
// initialised (but unregistered) monitor, ready for direct Reconcile calls in unit tests.
func newTestRepairReconciler(c client.Client, vmSource reservations.VMSource) *RepairReconciler {
	return &RepairReconciler{
		Client:   c,
		Conf:     RepairReconcilerConfig{MinInterval: metav1.Duration{Duration: 1 * time.Hour}, MaxInterval: metav1.Duration{Duration: 24 * time.Hour}},
		VMSource: vmSource,
		Monitor:  NewRepairReconcilerMonitor(),
	}
}

// ============================================================================
// Tests: helper functions
// ============================================================================

func TestSortVMsDescByMemory(t *testing.T) {
	tests := []struct {
		name      string
		input     []reservations.VM
		wantOrder []string // expected UUID order after sort
	}{
		{
			name: "already sorted descending — no change",
			input: []reservations.VM{
				repairVM("vm-large", "h1", "p1", "large", 32768, 16),
				repairVM("vm-medium", "h1", "p1", "medium", 16384, 8),
				repairVM("vm-small", "h1", "p1", "small", 8192, 4),
			},
			wantOrder: []string{"vm-large", "vm-medium", "vm-small"},
		},
		{
			name: "reversed input — sorted descending",
			input: []reservations.VM{
				repairVM("vm-small", "h1", "p1", "small", 8192, 4),
				repairVM("vm-medium", "h1", "p1", "medium", 16384, 8),
				repairVM("vm-large", "h1", "p1", "large", 32768, 16),
			},
			wantOrder: []string{"vm-large", "vm-medium", "vm-small"},
		},
		{
			name: "equal memory — stable tie-break by UUID ascending",
			input: []reservations.VM{
				repairVM("vm-b", "h1", "p1", "small", 8192, 4),
				repairVM("vm-a", "h1", "p1", "small", 8192, 4),
			},
			wantOrder: []string{"vm-a", "vm-b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sortVMsDescByMemory(tc.input)
			for i, wantUUID := range tc.wantOrder {
				if tc.input[i].UUID != wantUUID {
					t.Errorf("position %d: want UUID %q, got %q", i, wantUUID, tc.input[i].UUID)
				}
			}
		})
	}
}

func TestFillSlotFromHost(t *testing.T) {
	// Slot: 32 GiB committed memory
	const slotMemGiB = 32

	makeSlot := func(existingMemMB uint64) *v1alpha1.Reservation {
		allocs := map[string]v1alpha1.CommittedResourceAllocation{}
		if existingMemMB > 0 {
			allocs["existing-vm"] = v1alpha1.CommittedResourceAllocation{
				CreationTimestamp: metav1.Now(),
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(int64(existingMemMB)*1024*1024, resource.BinarySI), //nolint:gosec
				},
			}
		}
		return &v1alpha1.Reservation{
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(slotMemGiB*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Allocations: allocs,
				},
			},
		}
	}

	tests := []struct {
		name          string
		existingMemMB uint64 // memory already assigned to the slot (0 = empty)
		vmsOnHost     []reservations.VM
		wantAssigned  bool
		wantNewVMIDs  []string // UUIDs expected in allocations after fill (order not checked)
	}{
		{
			name:          "empty slot, two VMs fit — both assigned (16+16=32 GiB)",
			existingMemMB: 0,
			vmsOnHost: []reservations.VM{
				repairVM("vm-a", "h1", "p1", "medium", 16384, 8),
				repairVM("vm-b", "h1", "p1", "medium", 16384, 8),
			},
			wantAssigned: true,
			wantNewVMIDs: []string{"vm-a", "vm-b"},
		},
		{
			name:          "partial slot (16 GiB used), one more medium fits",
			existingMemMB: 16384,
			vmsOnHost: []reservations.VM{
				repairVM("vm-new", "h1", "p1", "medium", 16384, 8),
			},
			wantAssigned: true,
			wantNewVMIDs: []string{"vm-new"},
		},
		{
			name:          "slot already full — no VMs assigned",
			existingMemMB: slotMemGiB * 1024,
			vmsOnHost: []reservations.VM{
				repairVM("vm-new", "h1", "p1", "medium", 16384, 8),
			},
			wantAssigned: false,
			wantNewVMIDs: nil,
		},
		{
			name:          "VM too large for remaining capacity — skipped",
			existingMemMB: 24 * 1024, // 24 GiB used, 8 GiB remaining
			vmsOnHost: []reservations.VM{
				repairVM("vm-big", "h1", "p1", "large", 32768, 16), // 32 GiB > 8 GiB remaining
			},
			wantAssigned: false,
			wantNewVMIDs: nil,
		},
		{
			name:          "mixed sizes — large skipped, small fits in remaining 8 GiB",
			existingMemMB: 24 * 1024, // 8 GiB remaining
			vmsOnHost: []reservations.VM{
				repairVM("vm-large", "h1", "p1", "large", 32768, 16),
				repairVM("vm-small", "h1", "p1", "small", 8192, 4),
			},
			wantAssigned: true,
			wantNewVMIDs: []string{"vm-small"},
		},
		{
			name:          "no VMs on host — nothing assigned",
			existingMemMB: 0,
			vmsOnHost:     nil,
			wantAssigned:  false,
			wantNewVMIDs:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slot := makeSlot(tc.existingMemMB)
			unassigned := map[string][]reservations.VM{
				"h1": tc.vmsOnHost,
			}
			exclusionSet := make(map[string]struct{})
			// Existing VM (if any) is already excluded.
			if tc.existingMemMB > 0 {
				exclusionSet["existing-vm"] = struct{}{}
			}

			got := fillSlotFromHost(slot, "h1", unassigned, exclusionSet)

			if got != tc.wantAssigned {
				t.Errorf("fillSlotFromHost returned %v, want %v", got, tc.wantAssigned)
			}
			for _, vmUUID := range tc.wantNewVMIDs {
				if _, ok := slot.Spec.CommittedResourceReservation.Allocations[vmUUID]; !ok {
					t.Errorf("expected VM %q in slot allocations, not found", vmUUID)
				}
				if _, ok := exclusionSet[vmUUID]; !ok {
					t.Errorf("expected VM %q in exclusionSet after assignment, not found", vmUUID)
				}
			}
			// Assigned VMs must be removed from unassigned.
			for _, vmUUID := range tc.wantNewVMIDs {
				for _, remaining := range unassigned["h1"] {
					if remaining.UUID == vmUUID {
						t.Errorf("VM %q should have been removed from unassigned after assignment", vmUUID)
					}
				}
			}
		})
	}
}

// ============================================================================
// Tests: RepairReconciler.Reconcile
// ============================================================================

func TestRepairReconciler_SkipsOnCooldown(t *testing.T) {
	recentRepair := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	cr := newRepairCR("cr-1", "uuid-1")
	cr.Status.LastRepairAt = &recentRepair

	k8sClient := newRepairTestClient(t, cr)
	r := newTestRepairReconciler(k8sClient, nil)

	res, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected non-zero RequeueAfter during cooldown")
	}
	// Status must not change.
	var updated v1alpha1.CommittedResource
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "cr-1"}, &updated); err != nil {
		t.Fatalf("get cr: %v", err)
	}
	if updated.Status.LastRepairAt == nil || !updated.Status.LastRepairAt.Time.Truncate(time.Second).Equal(recentRepair.Truncate(time.Second)) {
		t.Error("LastRepairAt should be unchanged during cooldown")
	}
}

func TestRepairReconciler_Phase1_StaleRemoval(t *testing.T) {
	tests := []struct {
		name               string
		hvActiveIDs        []string // instance IDs active on "host-1"
		slotAllocs         map[string]v1alpha1.CommittedResourceAllocation
		wantRemainingVMIDs []string // UUIDs expected to remain after stale removal
	}{
		{
			name:        "VM in HV CRD — kept",
			hvActiveIDs: []string{"vm-1"},
			slotAllocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-1": {CreationTimestamp: metav1.Now()},
			},
			wantRemainingVMIDs: []string{"vm-1"},
		},
		{
			name:        "VM not in HV CRD — removed",
			hvActiveIDs: []string{},
			slotAllocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-gone": {CreationTimestamp: metav1.Now()},
			},
			wantRemainingVMIDs: nil,
		},
		{
			name:        "one stale, one live — stale removed, live kept",
			hvActiveIDs: []string{"vm-live"},
			slotAllocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-live":  {CreationTimestamp: metav1.Now()},
				"vm-stale": {CreationTimestamp: metav1.Now()},
			},
			wantRemainingVMIDs: []string{"vm-live"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := newRepairCR("cr-1", "uuid-1")
			slot := newRepairSlot("slot-1", "uuid-1", "host-1", 32, tc.slotAllocs)
			hv := repairHV("host-1", tc.hvActiveIDs...)
			knowledge := newRepairFlavorKnowledge(t)

			k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
			r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: nil})

			_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
			if err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			allocs := slotAllocs(t, k8sClient, "slot-1")
			if len(tc.wantRemainingVMIDs) == 0 {
				if len(allocs) != 0 {
					t.Errorf("expected empty allocations, got %v", allocs)
				}
			} else {
				for _, vmUUID := range tc.wantRemainingVMIDs {
					if _, ok := allocs[vmUUID]; !ok {
						t.Errorf("expected VM %q to remain, not found in allocs %v", vmUUID, allocs)
					}
				}
				if len(allocs) != len(tc.wantRemainingVMIDs) {
					t.Errorf("unexpected extra allocations: got %v, want %v", allocs, tc.wantRemainingVMIDs)
				}
			}
		})
	}
}

func TestRepairReconciler_Phase1_DedupAcrossSlots(t *testing.T) {
	tests := []struct {
		name           string
		slot1Allocs    map[string]v1alpha1.CommittedResourceAllocation
		slot2Allocs    map[string]v1alpha1.CommittedResourceAllocation
		hv1ActiveIDs   []string // active instance IDs on host-1
		hv2ActiveIDs   []string // active instance IDs on host-2
		wantSlot1VMIDs []string
		wantSlot2VMIDs []string
	}{
		{
			name: "VM in two slots of same CR — removed from both",
			slot1Allocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-dup": {CreationTimestamp: metav1.Now()},
				"vm-ok":  {CreationTimestamp: metav1.Now()},
			},
			slot2Allocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-dup": {CreationTimestamp: metav1.Now()},
			},
			hv1ActiveIDs:   []string{"vm-dup", "vm-ok"},
			hv2ActiveIDs:   []string{"vm-dup"},
			wantSlot1VMIDs: []string{"vm-ok"},
			wantSlot2VMIDs: nil,
		},
		{
			name: "no duplicates — all allocations kept",
			slot1Allocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-a": {CreationTimestamp: metav1.Now()},
			},
			slot2Allocs: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-b": {CreationTimestamp: metav1.Now()},
			},
			hv1ActiveIDs:   []string{"vm-a"},
			hv2ActiveIDs:   []string{"vm-b"},
			wantSlot1VMIDs: []string{"vm-a"},
			wantSlot2VMIDs: []string{"vm-b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := newRepairCR("cr-1", "uuid-1")
			slot1 := newRepairSlot("slot-1", "uuid-1", "host-1", 32, tc.slot1Allocs)
			slot2 := newRepairSlot("slot-2", "uuid-1", "host-2", 32, tc.slot2Allocs)
			hv1obj := repairHV("host-1", tc.hv1ActiveIDs...)
			hv2obj := repairHV("host-2", tc.hv2ActiveIDs...)
			knowledge := newRepairFlavorKnowledge(t)

			k8sClient := newRepairTestClient(t, cr, slot1, slot2, hv1obj, hv2obj, knowledge)
			r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: nil})

			_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
			if err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			checkAllocs := func(slotName string, wantUUIDs []string) {
				allocs := slotAllocs(t, k8sClient, slotName)
				if len(wantUUIDs) == 0 {
					if len(allocs) != 0 {
						t.Errorf("%s: expected empty allocations, got %v", slotName, allocs)
					}
					return
				}
				for _, uuid := range wantUUIDs {
					if _, ok := allocs[uuid]; !ok {
						t.Errorf("%s: expected VM %q, not found in %v", slotName, uuid, allocs)
					}
				}
				if len(allocs) != len(wantUUIDs) {
					t.Errorf("%s: unexpected allocations %v, want %v", slotName, allocs, wantUUIDs)
				}
			}
			checkAllocs("slot-1", tc.wantSlot1VMIDs)
			checkAllocs("slot-2", tc.wantSlot2VMIDs)
		})
	}
}

func TestRepairReconciler_Phase2_FillPartialSlot(t *testing.T) {
	tests := []struct {
		name          string
		existingAlloc map[string]v1alpha1.CommittedResourceAllocation
		azVMs         []reservations.VM // VMs returned by VMSource (all on host-1, project-1, test-group)
		wantVMInSlot  string            // UUID of newly added VM (empty = expect no change)
	}{
		{
			name: "partial slot (16 GiB used) + 16 GiB VM on same host — VM assigned",
			existingAlloc: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-existing": {
					CreationTimestamp: metav1.Now(),
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			azVMs: []reservations.VM{
				repairVM("vm-new", "host-1", "project-1", "medium", 16384, 8),
			},
			wantVMInSlot: "vm-new",
		},
		{
			name: "partial slot + no unassigned VMs — slot unchanged",
			existingAlloc: map[string]v1alpha1.CommittedResourceAllocation{
				"vm-existing": {
					CreationTimestamp: metav1.Now(),
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			azVMs:        nil,
			wantVMInSlot: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := newRepairCR("cr-1", "uuid-1")
			slot := newRepairSlot("slot-1", "uuid-1", "host-1", 32, tc.existingAlloc)
			hv := repairHV("host-1", "vm-existing", "vm-new")
			knowledge := newRepairFlavorKnowledge(t)

			k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
			r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: tc.azVMs})

			_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
			if err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			allocs := slotAllocs(t, k8sClient, "slot-1")
			if tc.wantVMInSlot != "" {
				if _, ok := allocs[tc.wantVMInSlot]; !ok {
					t.Errorf("expected VM %q to be assigned to slot, allocs: %v", tc.wantVMInSlot, allocs)
				}
			} else {
				// Only the pre-existing VM should be present.
				for uuid := range allocs {
					if _, preexisting := tc.existingAlloc[uuid]; !preexisting {
						t.Errorf("unexpected VM %q added to slot", uuid)
					}
				}
			}
		})
	}
}

func TestRepairReconciler_Phase2_EmptySlotRelocation(t *testing.T) {
	tests := []struct {
		name            string
		slot1TargetHost string
		azVMs           []reservations.VM
		wantSlot1Host   string // expected TargetHost after reconcile
		wantSlot1HasVMs bool   // expect at least one VM assigned
	}{
		{
			name:            "empty slot on host with VMs — filled in place",
			slot1TargetHost: "host-1",
			azVMs: []reservations.VM{
				repairVM("vm-a", "host-1", "project-1", "medium", 16384, 8),
			},
			wantSlot1Host:   "host-1",
			wantSlot1HasVMs: true,
		},
		{
			name:            "empty slot on host with no VMs, VMs on host-2 — relocated",
			slot1TargetHost: "host-1",
			azVMs: []reservations.VM{
				repairVM("vm-b", "host-2", "project-1", "medium", 16384, 8),
			},
			wantSlot1Host:   "host-2",
			wantSlot1HasVMs: true,
		},
		{
			name:            "empty slot, no VMs anywhere — unchanged",
			slot1TargetHost: "host-1",
			azVMs:           nil,
			wantSlot1Host:   "host-1",
			wantSlot1HasVMs: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := newRepairCR("cr-1", "uuid-1")
			slot := newRepairSlot("slot-1", "uuid-1", tc.slot1TargetHost, 32, nil)
			hv1obj := repairHV("host-1") // no active instances
			hv2obj := repairHV("host-2", "vm-b")
			knowledge := newRepairFlavorKnowledge(t)

			k8sClient := newRepairTestClient(t, cr, slot, hv1obj, hv2obj, knowledge)
			r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: tc.azVMs})

			_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
			if err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			gotHost := slotTargetHost(t, k8sClient, "slot-1")
			if gotHost != tc.wantSlot1Host {
				t.Errorf("TargetHost: want %q, got %q", tc.wantSlot1Host, gotHost)
			}
			allocs := slotAllocs(t, k8sClient, "slot-1")
			hasVMs := len(allocs) > 0
			if hasVMs != tc.wantSlot1HasVMs {
				t.Errorf("hasVMs: want %v, got %v (allocs=%v)", tc.wantSlot1HasVMs, hasVMs, allocs)
			}
		})
	}
}

// TestRepairReconciler_CrossProjectIsolation verifies that VMs from other projects
// are never assigned to slots of the current project.
func TestRepairReconciler_CrossProjectIsolation(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	slot := newRepairSlot("slot-1", "uuid-1", "host-1", 32, nil)
	hv := repairHV("host-1", "vm-other-project", "vm-mine")
	knowledge := newRepairFlavorKnowledge(t)

	// Two VMs on the same host: one belongs to the correct project, one to a different project.
	azVMs := []reservations.VM{
		repairVM("vm-other-project", "host-1", "other-project", "medium", 16384, 8),
		repairVM("vm-mine", "host-1", "project-1", "medium", 16384, 8),
	}

	k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: azVMs})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs := slotAllocs(t, k8sClient, "slot-1")
	if _, ok := allocs["vm-other-project"]; ok {
		t.Error("VM from wrong project was assigned to slot")
	}
	if _, ok := allocs["vm-mine"]; !ok {
		t.Error("VM from correct project was not assigned to slot")
	}
}

// TestRepairReconciler_CrossFlavorGroupIsolation verifies that VMs of a different flavor
// group are not assigned even if the project matches.
func TestRepairReconciler_CrossFlavorGroupIsolation(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	slot := newRepairSlot("slot-1", "uuid-1", "host-1", 32, nil)
	hv := repairHV("host-1", "vm-wrong-flavor", "vm-correct")
	knowledge := newRepairFlavorKnowledge(t)

	azVMs := []reservations.VM{
		repairVM("vm-wrong-flavor", "host-1", "project-1", "other-flavor-group", 16384, 8),
		repairVM("vm-correct", "host-1", "project-1", "medium", 16384, 8),
	}

	k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: azVMs})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs := slotAllocs(t, k8sClient, "slot-1")
	if _, ok := allocs["vm-wrong-flavor"]; ok {
		t.Error("VM with wrong flavor group was assigned to slot")
	}
	if _, ok := allocs["vm-correct"]; !ok {
		t.Error("VM with correct flavor was not assigned")
	}
}

// TestRepairReconciler_DedupAcrossSiblingCRs verifies that a VM assigned to a slot in
// one CR is not also assigned to a slot in a sibling CR (same project+flavorGroup).
func TestRepairReconciler_DedupAcrossSiblingCRs(t *testing.T) {
	// Two CRs for the same project+flavorGroup, each with one slot.
	// vm-shared appears in both slots — it must be removed from both by Phase 1b.
	cr1 := newRepairCR("cr-1", "uuid-1")
	cr2 := newRepairCR("cr-2", "uuid-2")
	slot1 := newRepairSlot("slot-1", "uuid-1", "host-1", 32, map[string]v1alpha1.CommittedResourceAllocation{
		"vm-shared":      {CreationTimestamp: metav1.Now()},
		"vm-only-in-cr1": {CreationTimestamp: metav1.Now()},
	})
	slot2 := newRepairSlot("slot-2", "uuid-2", "host-2", 32, map[string]v1alpha1.CommittedResourceAllocation{
		"vm-shared": {CreationTimestamp: metav1.Now()},
	})
	hv1obj := repairHV("host-1", "vm-shared", "vm-only-in-cr1")
	hv2obj := repairHV("host-2", "vm-shared")
	knowledge := newRepairFlavorKnowledge(t)

	k8sClient := newRepairTestClient(t, cr1, cr2, slot1, slot2, hv1obj, hv2obj, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: nil})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs1 := slotAllocs(t, k8sClient, "slot-1")
	allocs2 := slotAllocs(t, k8sClient, "slot-2")

	// vm-shared must be gone from both slots.
	if _, ok := allocs1["vm-shared"]; ok {
		t.Error("vm-shared should be removed from slot-1 (dedup)")
	}
	if _, ok := allocs2["vm-shared"]; ok {
		t.Error("vm-shared should be removed from slot-2 (dedup)")
	}
	// vm-only-in-cr1 is not a duplicate — it must stay.
	if _, ok := allocs1["vm-only-in-cr1"]; !ok {
		t.Error("vm-only-in-cr1 should remain in slot-1")
	}
}

// TestRepairReconciler_PlacementModeSlotClearedAllocations verifies that a slot currently
// in placement mode (no TargetHost) has any stale allocations removed — the slot cannot
// be validated against a host, so any existing allocations are treated as orphaned.
func TestRepairReconciler_PlacementModeSlotClearedAllocations(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	// Slot has allocations but no TargetHost — currently in placement mode.
	slot := newRepairSlot("slot-1", "uuid-1", "", 32, map[string]v1alpha1.CommittedResourceAllocation{
		"orphaned-vm": {CreationTimestamp: metav1.Now()},
	})
	hv := repairHV("host-1") // orphaned-vm is not listed as active on any HV
	knowledge := newRepairFlavorKnowledge(t)

	k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: nil})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs := slotAllocs(t, k8sClient, "slot-1")
	if _, ok := allocs["orphaned-vm"]; ok {
		t.Error("orphaned-vm should have been removed from placement-mode slot (no TargetHost)")
	}
}

// TestRepairReconciler_NoDoubleAssignmentAcrossEmptySlots verifies that when two empty
// slots need filling and only one unassigned VM is available, the VM is assigned to exactly
// one slot, not both.
func TestRepairReconciler_NoDoubleAssignmentAcrossEmptySlots(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	slot1 := newRepairSlot("slot-1", "uuid-1", "host-1", 32, nil)
	slot2 := newRepairSlot("slot-2", "uuid-1", "host-1", 32, nil)
	hv := repairHV("host-1", "vm-only")
	knowledge := newRepairFlavorKnowledge(t)

	// Only one VM available for two empty slots.
	azVMs := []reservations.VM{
		repairVM("vm-only", "host-1", "project-1", "medium", 16384, 8),
	}

	k8sClient := newRepairTestClient(t, cr, slot1, slot2, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: azVMs})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs1 := slotAllocs(t, k8sClient, "slot-1")
	allocs2 := slotAllocs(t, k8sClient, "slot-2")

	totalAssignments := 0
	if _, ok := allocs1["vm-only"]; ok {
		totalAssignments++
	}
	if _, ok := allocs2["vm-only"]; ok {
		totalAssignments++
	}
	if totalAssignments != 1 {
		t.Errorf("vm-only should be assigned to exactly one slot, got %d assignments (slot1=%v, slot2=%v)",
			totalAssignments, allocs1, allocs2)
	}
}

// TestRepairReconciler_AlreadyAssignedVMNotReassigned verifies that a VM already assigned
// to a slot (and in the exclusion set) is not added to a second slot during Phase 2.
func TestRepairReconciler_AlreadyAssignedVMNotReassigned(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	// slot-1 already has vm-assigned. slot-2 is empty and on a different host.
	slot1 := newRepairSlot("slot-1", "uuid-1", "host-1", 32, map[string]v1alpha1.CommittedResourceAllocation{
		"vm-assigned": {
			CreationTimestamp: metav1.Now(),
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
			},
		},
	})
	slot2 := newRepairSlot("slot-2", "uuid-1", "host-1", 32, nil)
	hv := repairHV("host-1", "vm-assigned")
	knowledge := newRepairFlavorKnowledge(t)

	// vm-assigned is active on host-1 and matches the project+flavor.
	// It should NOT be moved to slot-2 because it's already in slot-1.
	azVMs := []reservations.VM{
		repairVM("vm-assigned", "host-1", "project-1", "medium", 16384, 8),
	}

	k8sClient := newRepairTestClient(t, cr, slot1, slot2, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: azVMs})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	allocs1 := slotAllocs(t, k8sClient, "slot-1")
	allocs2 := slotAllocs(t, k8sClient, "slot-2")

	if _, ok := allocs1["vm-assigned"]; !ok {
		t.Error("vm-assigned was incorrectly removed from slot-1")
	}
	if _, ok := allocs2["vm-assigned"]; ok {
		t.Error("vm-assigned was incorrectly double-assigned to slot-2")
	}
}

func TestRepairReconciler_StatusUpdated(t *testing.T) {
	cr := newRepairCR("cr-1", "uuid-1")
	slot := newRepairSlot("slot-1", "uuid-1", "host-1", 32, nil)
	hv := repairHV("host-1")
	knowledge := newRepairFlavorKnowledge(t)

	k8sClient := newRepairTestClient(t, cr, slot, hv, knowledge)
	r := newTestRepairReconciler(k8sClient, &fakeVMSource{vms: nil})

	_, err := r.Reconcile(context.Background(), repairReconcilerReq("cr-1"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var updated v1alpha1.CommittedResource
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "cr-1"}, &updated); err != nil {
		t.Fatalf("get cr: %v", err)
	}
	if updated.Status.LastRepairAt == nil {
		t.Error("LastRepairAt should be set after reconcile")
	}
	if updated.Status.LastRepairAt.IsZero() {
		t.Error("LastRepairAt should not be zero time")
	}
}

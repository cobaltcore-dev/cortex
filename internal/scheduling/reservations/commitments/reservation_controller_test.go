// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	sched "github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// fakePipeline returns a fixed ordered-host list (modelling a scheduler run).
type fakePipeline struct{ hosts []string }

func (p fakePipeline) Run(sched.PlacementRequest) (sched.PlacementResult, error) {
	return sched.PlacementResult{OrderedHosts: p.hosts}, nil
}

// fakePipelineProvider serves a fakePipeline for every pipeline name, returning
// the configured host list.
type fakePipelineProvider struct{ hosts []string }

func (f fakePipelineProvider) GetPipeline(string) (lib.FilterWeigherPipeline[sched.PlacementRequest], bool) {
	return fakePipeline(f), true
}

// fakeGatherer is a no-op gatherer (tests supply their own hosts).
type fakeGatherer struct{}

func (fakeGatherer) MutateWithAllCandidates(context.Context, *sched.PlacementRequest) error {
	return nil
}

// newMockScheduler builds an in-process compute.Placement whose pipeline returns
// the given host list. Replaces the former httptest mock scheduler server.
func newMockScheduler(hosts []string) *sched.Placement {
	return sched.NewPlacement(fakePipelineProvider{hosts: hosts}, fakeGatherer{})
}

// mockDomainResolver is a test double for DomainResolver.
type mockDomainResolver struct {
	// names maps domainID → name returned on success.
	names map[string]string
	// err is returned for all calls when non-nil.
	err error
	// calls records every domainID passed to ResolveDomainName.
	calls []string
}

func (m *mockDomainResolver) ResolveDomainName(_ context.Context, domainID string) (string, error) {
	m.calls = append(m.calls, domainID)
	if m.err != nil {
		return "", m.err
	}
	return m.names[domainID], nil
}

// vmSourceByUUID is a test VMSource that looks up VMs by UUID from a fixed map.
// Used by reconcileAllocations tests to replace the former HV CRD presence check.
type vmSourceByUUID struct {
	vms map[string]*reservations.VM
}

func (s *vmSourceByUUID) GetVM(_ context.Context, uuid string) (*reservations.VM, error) {
	if s.vms == nil {
		return nil, nil
	}
	return s.vms[uuid], nil
}
func (s *vmSourceByUUID) ListVMs(_ context.Context) ([]reservations.VM, error) { return nil, nil }
func (s *vmSourceByUUID) ListVMsByProject(_ context.Context, _ string) ([]reservations.VM, error) {
	return nil, nil
}
func (s *vmSourceByUUID) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]reservations.VM, error) {
	return nil, nil
}
func (s *vmSourceByUUID) IsServerActive(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *vmSourceByUUID) GetDeletedVMInfo(_ context.Context, _ string) (*reservations.DeletedVMInfo, error) {
	return nil, nil
}

func TestCommitmentReservationController_Reconcile(t *testing.T) {
	scheme := newCRTestScheme(t)

	tests := []struct {
		name          string
		reservation   *v1alpha1.Reservation
		expectedReady bool
		expectedError string
		shouldRequeue bool
	}{
		{
			name: "expect already active reservation",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID:    "test-project",
						ResourceName: "test-flavor",
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "host-1",
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReservationConditionReady,
							Status: metav1.ConditionTrue,
							Reason: "ReservationActive",
						},
					},
				},
			},
			expectedReady: true,
			shouldRequeue: true,
		},
		{
			name: "skip reservation without resource name",
			reservation: &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: v1alpha1.ReservationSpec{},
			},
			expectedReady: false,
			expectedError: "reservation has no resource name",
			shouldRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := newCRTestClient(scheme, tt.reservation)

			reconciler := &CommitmentReservationController{
				Client: k8sClient,
				Scheme: scheme,
				Conf: ReservationControllerConfig{
					RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
				},
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.reservation.Name,
				},
			}

			result, err := reconciler.Reconcile(context.Background(), req)

			if err != nil {
				t.Errorf("Reconcile() error = %v", err)
				return
			}

			if tt.shouldRequeue && result.RequeueAfter == 0 {
				t.Errorf("Expected requeue but got none")
			}

			if !tt.shouldRequeue && result.RequeueAfter > 0 {
				t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
			}

			var updated v1alpha1.Reservation
			err = k8sClient.Get(context.Background(), req.NamespacedName, &updated)
			if err != nil {
				t.Errorf("Failed to get updated reservation: %v", err)
				return
			}

			isReady := meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
			if isReady != tt.expectedReady {
				t.Errorf("Expected ready=%v, got ready=%v", tt.expectedReady, isReady)
			}

			if tt.expectedError != "" {
				cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
				if cond == nil || cond.Status != metav1.ConditionFalse {
					t.Errorf("Expected Ready=False with error, got %v", updated.Status.Conditions)
				}
			}
		})
	}
}

// TestMigrateSpecAllocationsToAnnotation verifies that a blind-scheduler slot that has
// Spec.CommittedResourceReservation.Allocation populated (old schema) but no
// AnnotationCandidateAllocation annotation gets the annotation seeded from the first
// Spec allocation entry on reconcile (one-time upgrade migration path).
func TestMigrateSpecAllocationsToAnnotation(t *testing.T) {
	scheme := newCRTestScheme(t)

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "slot-blind",
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "host-1",
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("8Gi"),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
				Allocations: map[string]v1alpha1.CommittedResourceAllocation{ //nolint:staticcheck
					"vm-legacy-b": {ObjectRef: corev1.ObjectReference{Name: "vm-legacy-b"}},
					"vm-legacy-a": {ObjectRef: corev1.ObjectReference{Name: "vm-legacy-a"}},
				},
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReservationConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "ReservationActive",
					ObservedGeneration: 1,
				},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{},
		},
	}

	k8sClient := newCRTestClient(scheme, res)
	reconciler := &CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf: ReservationControllerConfig{
			RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
		},
		VMSource: &vmSourceByUUID{},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var updated v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Lexically first UUID is "vm-legacy-a".
	got := updated.Annotations[v1alpha1.AnnotationCandidateAllocation]
	if got != "vm-legacy-a" {
		t.Errorf("expected annotation %s=vm-legacy-a, got %q", v1alpha1.AnnotationCandidateAllocation, got)
	}
}

// ============================================================================
// Test: reconcileAllocations
// ============================================================================

func TestReconcileAllocations(t *testing.T) {
	scheme := newCRTestScheme(t)

	type tc struct {
		name           string
		res            *v1alpha1.Reservation
		siblings       []*v1alpha1.Reservation
		vms            map[string]*reservations.VM
		vmSrc          reservations.VMSource // overrides vms when set
		secondResName  string                // if set, fetch fresh + reconcile as second pass
		wantAllocKeys  []string
		wantAbsentKeys []string
		wantAllocGiB   map[string]int64  // vmUUID → GiB for memory resource
		wantObjectRef  map[string]string // vmUUID → expected ObjectRef.Name
		extraCheck     func(t *testing.T, k8sClient client.Client)
	}

	tests := []tc{
		{
			name: "confirmed VM present on expected host",
			res:  withAlloc(crSlot("test-reservation", "host-1", 8, nil), "vm-1", 8),
			vms: map[string]*reservations.VM{
				"vm-1": {UUID: "vm-1", CurrentHypervisor: "host-1", Resources: map[string]resource.Quantity{string(hv1.ResourceMemory): testGiB(8)}},
			},
			wantAllocKeys: []string{"vm-1"},
			wantAllocGiB:  map[string]int64{"vm-1": 8},
		},
		{
			name: "confirmed inactive VM still counted (stopped/shelved)",
			res:  withAlloc(crSlot("test-reservation", "host-1", 8, nil), "vm-stopped", 8),
			vms: map[string]*reservations.VM{
				"vm-stopped": {UUID: "vm-stopped", CurrentHypervisor: "host-1", Resources: map[string]resource.Quantity{string(hv1.ResourceMemory): testGiB(8)}},
			},
			wantAllocKeys: []string{"vm-stopped"},
		},
		{
			name:           "confirmed VM absent from VMSource — removed",
			res:            withAlloc(crSlot("test-reservation", "host-1", 8, nil), "vm-gone", 8),
			vms:            map[string]*reservations.VM{},
			wantAbsentKeys: []string{"vm-gone"},
		},
		{
			name: "empty allocations — no work to do",
			res:  crSlot("test-reservation", "host-1", 8, nil),
			vms:  map[string]*reservations.VM{},
		},
		{
			name: "confirmed VM stays confirmed — idempotent re-reconcile",
			res:  withAlloc(crSlot("res-winning", "host-1", 8, nil), "vm-confirmed-uuid", 8),
			vms: map[string]*reservations.VM{
				"vm-confirmed-uuid": {UUID: "vm-confirmed-uuid", CurrentHypervisor: "host-1", Resources: map[string]resource.Quantity{string(hv1.ResourceMemory): testGiB(8)}},
			},
			wantAllocKeys: []string{"vm-confirmed-uuid"},
			wantAllocGiB:  map[string]int64{"vm-confirmed-uuid": 8},
		},
		{
			name: "annotation candidate confirmed — ObjectRef.Name set correctly",
			res: crSlot("res-candidate", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-new-candidate",
			}),
			vms: map[string]*reservations.VM{
				"vm-new-candidate": {UUID: "vm-new-candidate", CurrentHypervisor: "host-1", Resources: map[string]resource.Quantity{string(hv1.ResourceMemory): testGiB(8)}},
			},
			wantAllocKeys: []string{"vm-new-candidate"},
			wantObjectRef: map[string]string{"vm-new-candidate": "vm-new-candidate"},
		},
		{
			// S02: min taken per resource dimension; slot caps cpu even though VM has more.
			name: "S02 multi-dimensional resources — min taken per dimension",
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "slot-a",
					Annotations: map[string]string{v1alpha1.AnnotationCandidateAllocation: "vm-multi"},
				},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: testGiB(8),
						hv1.ResourceCPU:    resource.MustParse("4"),
					},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID: "p-1", ResourceName: "test-flavor", PrimaryResource: hv1.ResourceMemory,
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host:                         "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{},
				},
			},
			vms: map[string]*reservations.VM{
				"vm-multi": {UUID: "vm-multi", CurrentHypervisor: "host-1", Resources: map[string]resource.Quantity{
					string(hv1.ResourceMemory): testGiB(8),
					string(hv1.ResourceCPU):    resource.MustParse("8"),
				}},
			},
			wantAllocKeys: []string{"vm-multi"},
			wantAllocGiB:  map[string]int64{"vm-multi": 8},
			extraCheck: func(t *testing.T, k8sClient client.Client) {
				t.Helper()
				var updated v1alpha1.Reservation
				if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "slot-a"}, &updated); err != nil {
					t.Fatalf("get slot-a: %v", err)
				}
				alloc := updated.Status.CommittedResourceReservation.Allocation["vm-multi"]
				got := alloc.Resources[hv1.ResourceCPU]
				if got.Cmp(resource.MustParse("4")) != 0 {
					t.Errorf("cpu alloc: want 4, got %s", got.String())
				}
			},
		},
		{
			// S03: VM is larger than the slot; allocation is capped at slot capacity.
			name: "S03 VM too large for slot — allocation capped at slot capacity",
			res: crSlot("slot-a", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-big",
			}),
			vms:           map[string]*reservations.VM{"vm-big": memVM("vm-big", "host-1", 24)},
			wantAllocKeys: []string{"vm-big"},
			wantAllocGiB:  map[string]int64{"vm-big": 8},
		},
		{
			// S04: Annotated VM is on a different host — not confirmed.
			name: "S04 annotation candidate on wrong host — not confirmed",
			res: crSlot("slot-a", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-wrong-host",
			}),
			vms:            map[string]*reservations.VM{"vm-wrong-host": memVM("vm-wrong-host", "host-2", 8)},
			wantAbsentKeys: []string{"vm-wrong-host"},
		},
		{
			// S05: Previously confirmed VM disappears from VMSource — removed.
			name:           "S05 confirmed VM gone — removed from allocations",
			res:            withAlloc(crSlot("slot-a", "host-1", 8, nil), "vm-gone", 8),
			vms:            map[string]*reservations.VM{},
			wantAbsentKeys: []string{"vm-gone"},
		},
		{
			// S06: Two VMs on a 12 GiB slot; lexical order determines spare assignment.
			// vm-aaa (lexically first) gets 8 GiB; vm-zzz gets the remaining 4 GiB.
			name: "S06 two VMs on same slot — lexical order determines spare",
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "slot-a"},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(12)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID: "p-1", ResourceName: "test-flavor", PrimaryResource: hv1.ResourceMemory,
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocation: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-aaa": {ObjectRef: corev1.ObjectReference{Name: "vm-aaa"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)}},
							"vm-zzz": {ObjectRef: corev1.ObjectReference{Name: "vm-zzz"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)}},
						},
					},
				},
			},
			vms: map[string]*reservations.VM{
				"vm-aaa": memVM("vm-aaa", "host-1", 8),
				"vm-zzz": memVM("vm-zzz", "host-1", 8),
			},
			wantAllocGiB: map[string]int64{"vm-aaa": 8, "vm-zzz": 4},
		},
		{
			// S07: VM (16 GiB) spans two slots; slot-a already covers 8 GiB, slot-b gets the rest.
			name: "S07 VM spans two slots — second slot gets remainder",
			res: crSlot("slot-b", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-big",
			}),
			siblings:      []*v1alpha1.Reservation{withAlloc(crSlot("slot-a", "host-1", 8, nil), "vm-big", 8)},
			vms:           map[string]*reservations.VM{"vm-big": memVM("vm-big", "host-1", 16)},
			wantAllocKeys: []string{"vm-big"},
			wantAllocGiB:  map[string]int64{"vm-big": 8},
		},
		{
			// S08: Slot is fully occupied; annotation candidate cannot fit.
			name: "S08 slot at capacity — annotation candidate not confirmed",
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "slot-a",
					Annotations: map[string]string{v1alpha1.AnnotationCandidateAllocation: "vm-new"},
				},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID: "p-1", ResourceName: "test-flavor", PrimaryResource: hv1.ResourceMemory,
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocation: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-existing": {ObjectRef: corev1.ObjectReference{Name: "vm-existing"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)}},
						},
					},
				},
			},
			vms: map[string]*reservations.VM{
				"vm-existing": memVM("vm-existing", "host-1", 8),
				"vm-new":      memVM("vm-new", "host-1", 8),
			},
			wantAllocGiB:   map[string]int64{"vm-existing": 8},
			wantAbsentKeys: []string{"vm-new"},
		},
		{
			// S09: VMSource returns error for one VM; that VM is dropped, others are unaffected.
			name: "S09 VMSource error for one VM — dropped, others unaffected",
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "slot-a"},
				Spec: v1alpha1.ReservationSpec{
					Type:       v1alpha1.ReservationTypeCommittedResource,
					TargetHost: "host-1",
					Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(16)},
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID: "p-1", ResourceName: "test-flavor", PrimaryResource: hv1.ResourceMemory,
					},
				},
				Status: v1alpha1.ReservationStatus{
					Host: "host-1",
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
						Allocation: map[string]v1alpha1.CommittedResourceAllocation{
							"vm-good":  {ObjectRef: corev1.ObjectReference{Name: "vm-good"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)}},
							"vm-error": {ObjectRef: corev1.ObjectReference{Name: "vm-error"}, Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(8)}},
						},
					},
				},
			},
			vmSrc: &errForUUID{
				errUUID:  "vm-error",
				err:      errors.New("nova unavailable"),
				fallback: &vmSourceByUUID{vms: map[string]*reservations.VM{"vm-good": memVM("vm-good", "host-1", 8)}},
			},
			wantAllocGiB:   map[string]int64{"vm-good": 8},
			wantAbsentKeys: []string{"vm-error"},
		},
		{
			// ZeroAllocRemoval: VM fully covered by sibling slot — removed from this slot.
			name:           "zero alloc removal — VM fully covered by sibling slot removed",
			res:            withAlloc(crSlot("res-b", "host-1", 8, nil), "vm-race", 8),
			siblings:       []*v1alpha1.Reservation{withAlloc(crSlot("res-a", "host-1", 8, nil), "vm-race", 8)},
			vms:            map[string]*reservations.VM{"vm-race": memVM("vm-race", "host-1", 8)},
			wantAbsentKeys: []string{"vm-race"},
		},
		{
			// MultiSlotPartialAllocation: sibling covers 3 GiB of vm-1 (8 GiB VM); this slot gets the remaining 5 GiB.
			name: "partial allocation — sibling covers part, this slot gets remainder",
			res: crSlot("res-b", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-1",
			}),
			siblings:     []*v1alpha1.Reservation{withAlloc(crSlot("res-a", "host-1", 16, nil), "vm-1", 3)},
			vms:          map[string]*reservations.VM{"vm-1": memVM("vm-1", "host-1", 8)},
			wantAllocGiB: map[string]int64{"vm-1": 5},
		},
		{
			// S01: PAYG VM annotated on two slots simultaneously.
			// slot-a reconciles first and claims vm-x; slot-b reconciles second and defers.
			// Total assigned must equal VM size (8 GiB), exactly one slot wins.
			name: "S01 PAYG annotated on both slots — total = VM size, exactly one slot wins",
			res: crSlot("slot-a", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-x",
			}),
			siblings: []*v1alpha1.Reservation{crSlot("slot-b", "host-1", 8, map[string]string{
				v1alpha1.AnnotationCandidateAllocation: "vm-x",
			})},
			vms:           map[string]*reservations.VM{"vm-x": memVM("vm-x", "host-1", 8)},
			secondResName: "slot-b",
			wantAllocKeys: []string{"vm-x"},
			extraCheck: func(t *testing.T, k8sClient client.Client) {
				t.Helper()
				ctx := context.Background()
				var updA, updB v1alpha1.Reservation
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: "slot-a"}, &updA); err != nil {
					t.Fatalf("get slot-a: %v", err)
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: "slot-b"}, &updB); err != nil {
					t.Fatalf("get slot-b: %v", err)
				}
				allocA := getAllocGiB(&updA, "vm-x")
				allocB := getAllocGiB(&updB, "vm-x")
				total := allocA.DeepCopy()
				total.Add(allocB)
				if total.Cmp(testGiB(8)) != 0 {
					t.Errorf("total assigned to vm-x = %s, want 8 GiB (slot-a=%s, slot-b=%s)", total.String(), allocA.String(), allocB.String())
				}
				if (allocA.Cmp(testGiB(0)) > 0) == (allocB.Cmp(testGiB(0)) > 0) {
					t.Errorf("expected exactly one slot to hold vm-x, got slot-a=%s slot-b=%s", allocA.String(), allocB.String())
				}
			},
		},
		{
			// S10: Both slots start with vm-x confirmed (race state).
			// Step 1: slot-a reconciles, sees slot-b has vm-x → drops it.
			// Step 2: slot-b reconciles, sees slot-a has nothing → keeps vm-x.
			// Total = 8 GiB, exactly one slot wins.
			name:           "S10 convergence from race state — one slot drops, other keeps",
			res:            withAlloc(crSlot("slot-a", "host-1", 8, nil), "vm-x", 8),
			siblings:       []*v1alpha1.Reservation{withAlloc(crSlot("slot-b", "host-1", 8, nil), "vm-x", 8)},
			vms:            map[string]*reservations.VM{"vm-x": memVM("vm-x", "host-1", 8)},
			secondResName:  "slot-b",
			wantAbsentKeys: []string{"vm-x"},
			extraCheck: func(t *testing.T, k8sClient client.Client) {
				t.Helper()
				ctx := context.Background()
				var updA, updB v1alpha1.Reservation
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: "slot-a"}, &updA); err != nil {
					t.Fatalf("get slot-a: %v", err)
				}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: "slot-b"}, &updB); err != nil {
					t.Fatalf("get slot-b: %v", err)
				}
				allocA := getAllocGiB(&updA, "vm-x")
				allocB := getAllocGiB(&updB, "vm-x")
				total := allocA.DeepCopy()
				total.Add(allocB)
				if total.Cmp(testGiB(8)) != 0 {
					t.Errorf("total assigned to vm-x = %s, want exactly 8 GiB (slot-a=%s, slot-b=%s)", total.String(), allocA.String(), allocB.String())
				}
				if (allocA.Cmp(testGiB(0)) > 0) == (allocB.Cmp(testGiB(0)) > 0) {
					t.Errorf("expected exactly one slot to hold vm-x after convergence, got slot-a=%s slot-b=%s", allocA.String(), allocB.String())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.res}
			for _, s := range tt.siblings {
				objects = append(objects, s)
			}
			k8sClient := newCRTestClient(scheme, objects...)

			vmSrc := tt.vmSrc
			if vmSrc == nil {
				vmSrc = &vmSourceByUUID{vms: tt.vms}
			}
			controller := &CommitmentReservationController{
				Client:   k8sClient,
				Scheme:   scheme,
				Conf:     ReservationControllerConfig{},
				VMSource: vmSrc,
			}
			ctx := WithNewReconcileID(context.Background())

			if err := controller.reconcileAllocations(ctx, tt.res); err != nil {
				t.Fatalf("reconcileAllocations: %v", err)
			}

			if tt.secondResName != "" {
				var second v1alpha1.Reservation
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: tt.secondResName}, &second); err != nil {
					t.Fatalf("get second %q: %v", tt.secondResName, err)
				}
				if err := controller.reconcileAllocations(ctx, &second); err != nil {
					t.Fatalf("reconcileAllocations second=%q: %v", tt.secondResName, err)
				}
			}

			var updated v1alpha1.Reservation
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tt.res), &updated); err != nil {
				t.Fatalf("get updated: %v", err)
			}
			allocs := map[string]v1alpha1.CommittedResourceAllocation{}
			if updated.Status.CommittedResourceReservation != nil {
				allocs = updated.Status.CommittedResourceReservation.Allocation
			}

			for _, key := range tt.wantAllocKeys {
				if _, ok := allocs[key]; !ok {
					t.Errorf("want %q in allocations, not found", key)
				}
			}
			for _, key := range tt.wantAbsentKeys {
				if _, ok := allocs[key]; ok {
					t.Errorf("want %q absent from allocations, but found", key)
				}
			}
			for vmUUID, wantGiB := range tt.wantAllocGiB {
				alloc, ok := allocs[vmUUID]
				if !ok {
					t.Errorf("wantAllocGiB: %q not in allocations", vmUUID)
					continue
				}
				got := alloc.Resources[hv1.ResourceMemory]
				if got.Cmp(testGiB(wantGiB)) != 0 {
					t.Errorf("memory alloc for %q: want %d GiB, got %s", vmUUID, wantGiB, got.String())
				}
			}
			for vmUUID, wantName := range tt.wantObjectRef {
				alloc, ok := allocs[vmUUID]
				if !ok {
					t.Errorf("wantObjectRef: %q not in allocations", vmUUID)
					continue
				}
				if alloc.ObjectRef.Name != wantName {
					t.Errorf("ObjectRef.Name for %q: want %q, got %q", vmUUID, wantName, alloc.ObjectRef.Name)
				}
			}

			if tt.extraCheck != nil {
				tt.extraCheck(t, k8sClient)
			}
		})
	}
}

// ============================================================================
// Test: hypervisorToReservations mapper
// ============================================================================

// TestHypervisorToReservations tests the mapper that translates a Hypervisor change
// into reconcile requests for the CR reservations assigned to that host.
// This covers the mapper logic; the watch wiring itself (informer → mapper → enqueue)
// is controller-runtime's responsibility and is not unit-testable without envtest.
func TestHypervisorToReservations(t *testing.T) {
	scheme := newCRTestScheme(t)

	res1 := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-1"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}
	res2 := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-1b"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}
	resOtherHost := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-host-2"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
		Status:     v1alpha1.ReservationStatus{Host: "host-2"},
	}
	resNoHost := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-no-host"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource},
	}
	resFailover := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-failover"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover},
		Status:     v1alpha1.ReservationStatus{Host: "host-1"},
	}

	k8sClient := newCRTestClient(scheme, res1, res2, resOtherHost, resNoHost, resFailover)

	controller := &CommitmentReservationController{Client: k8sClient}

	hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
	requests := controller.hypervisorToReservations(context.Background(), hv)

	// Only CR reservations on host-1 should be enqueued; failover and other-host excluded
	got := make(map[string]bool, len(requests))
	for _, req := range requests {
		got[req.Name] = true
	}
	if len(got) != 2 {
		t.Errorf("expected 2 requests, got %d: %v", len(got), got)
	}
	for _, name := range []string{"res-host-1", "res-host-1b"} {
		if !got[name] {
			t.Errorf("expected %s in requests", name)
		}
	}
}

func TestCommitmentReservationController_reconcileInstanceReservation_Success(t *testing.T) {
	scheme := newCRTestScheme(t)

	reservation := &v1alpha1.Reservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    "test-project",
				ResourceName: "test-flavor",
			},
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("4Gi"),
				hv1.ResourceCPU:    resource.MustParse("2"),
			},
		},
	}

	hypervisor1 := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-1"}}
	hypervisor2 := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-2"}}

	k8sClient := newCRTestClient(scheme, reservation, newTestFlavorKnowledge(), hypervisor1, hypervisor2)

	reconciler := &CommitmentReservationController{
		Client:    k8sClient,
		Scheme:    scheme,
		Conf:      ReservationControllerConfig{},
		Scheduler: newMockScheduler([]string{"test-host-1", "test-host-2"}),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: reservation.Name,
		},
	}

	// First reconcile: schedules the reservation and sets Spec.TargetHost
	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("First reconcile error = %v", err)
		return
	}
	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue after first reconcile but got %v", result.RequeueAfter)
	}

	// Verify Spec.TargetHost is set after first reconcile
	var afterFirstReconcile v1alpha1.Reservation
	if err = k8sClient.Get(context.Background(), req.NamespacedName, &afterFirstReconcile); err != nil {
		t.Errorf("Failed to get reservation after first reconcile: %v", err)
		return
	}
	if afterFirstReconcile.Spec.TargetHost != "test-host-1" {
		t.Errorf("Expected Spec.TargetHost=%v after first reconcile, got %v", "test-host-1", afterFirstReconcile.Spec.TargetHost)
	}

	// Second reconcile: syncs Spec.TargetHost to Status and sets Ready=True
	result, err = reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Second reconcile error = %v", err)
		return
	}
	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue after second reconcile but got %v", result.RequeueAfter)
	}

	// Verify the reservation status after second reconcile
	var updated v1alpha1.Reservation
	if err = k8sClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	if !meta.IsStatusConditionTrue(updated.Status.Conditions, v1alpha1.ReservationConditionReady) {
		t.Errorf("Expected Ready=True, got %v", updated.Status.Conditions)
	}

	if updated.Status.Host != "test-host-1" {
		t.Errorf("Expected host %v, got %v", "test-host-1", updated.Status.Host)
	}
}

// ============================================================================
// Test: domain_name scheduler hint
// ============================================================================

// capturingPipeline records the SchedulerRequest it was run with and returns a
// single host, so tests can assert on the request the reconciler built.
type capturingPipeline struct{ captured *sched.PlacementRequest }

func (p capturingPipeline) Run(req sched.PlacementRequest) (sched.PlacementResult, error) {
	*p.captured = req
	return sched.PlacementResult{OrderedHosts: []string{"test-host-1"}}, nil
}

type capturingPipelineProvider struct{ captured *sched.PlacementRequest }

func (f capturingPipelineProvider) GetPipeline(string) (lib.FilterWeigherPipeline[sched.PlacementRequest], bool) {
	return capturingPipeline(f), true
}

func TestCommitmentReservationController_DomainNameHint(t *testing.T) {
	tests := []struct {
		name             string
		resolver         DomainResolver
		wantDomainName   string // empty means hint must be absent
		wantResolverCall string // expected domainID passed to resolver; empty means no call expected
	}{
		{
			name:             "hint present when resolver succeeds",
			resolver:         &mockDomainResolver{names: map[string]string{"domain-uuid-1": "monsoon3"}},
			wantDomainName:   "monsoon3",
			wantResolverCall: "domain-uuid-1",
		},
		{
			name:           "hint absent when resolver fails, scheduling continues",
			resolver:       &mockDomainResolver{err: errors.New("keystone unavailable")},
			wantDomainName: "",
		},
		{
			name:           "hint absent when resolver is nil (keystoneSecretRef not configured)",
			resolver:       nil,
			wantDomainName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newCRTestScheme(t)
			reservation := &v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{Name: "test-reservation"},
				Spec: v1alpha1.ReservationSpec{
					Type: v1alpha1.ReservationTypeCommittedResource,
					CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
						ProjectID:    "test-project",
						DomainID:     "domain-uuid-1",
						ResourceName: "test-flavor",
					},
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: resource.MustParse("4Gi"),
						hv1.ResourceCPU:    resource.MustParse("2"),
					},
				},
			}
			k8sClient := newCRTestClient(scheme, reservation, newTestFlavorKnowledge(),
				&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "test-host-1"}})

			var captured sched.PlacementRequest
			reconciler := &CommitmentReservationController{
				Client:         k8sClient,
				Scheme:         scheme,
				Conf:           ReservationControllerConfig{},
				Scheduler:      sched.NewPlacement(capturingPipelineProvider{captured: &captured}, fakeGatherer{}),
				DomainResolver: tt.resolver,
			}

			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: reservation.Name},
			}); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}

			// Verify resolver was called correctly.
			if tt.wantResolverCall != "" {
				m := tt.resolver.(*mockDomainResolver)
				if len(m.calls) != 1 || m.calls[0] != tt.wantResolverCall {
					t.Errorf("expected resolver called with %q, got %v", tt.wantResolverCall, m.calls)
				}
			}

			// Verify domain_name hint (now the typed request field).
			if captured.DomainName != tt.wantDomainName {
				t.Errorf("expected domain name %q, got %q", tt.wantDomainName, captured.DomainName)
			}

			// Intent must always be set regardless of resolver outcome.
			if captured.Intent != schedulerdelegationapi.ReserveForCommittedResourceIntent {
				t.Errorf("expected Intent=%q, got %q", schedulerdelegationapi.ReserveForCommittedResourceIntent, captured.Intent)
			}
		})
	}
}

func TestHvCapacityChangePredicate(t *testing.T) {
	gib := func(n int64) resource.Quantity { return *resource.NewQuantity(n*1024*1024*1024, resource.BinarySI) }
	cpu := func(n int64) resource.Quantity { return *resource.NewQuantity(n, resource.DecimalSI) }

	makeHV := func(memGiB, cpuCores int64, instances []hv1.Instance) hv1.Hypervisor {
		return hv1.Hypervisor{
			Status: hv1.HypervisorStatus{
				EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: gib(memGiB),
					hv1.ResourceCPU:    cpu(cpuCores),
				},
				Instances: instances,
			},
		}
	}

	tests := []struct {
		name     string
		old      hv1.Hypervisor
		new      hv1.Hypervisor
		wantFire bool
	}{
		{
			name:     "instances changed → fires",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(1024, 256, []hv1.Instance{{ID: "vm-1"}}),
			wantFire: true,
		},
		{
			name:     "effective capacity changed → fires",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(2048, 256, nil),
			wantFire: true,
		},
		{
			name: "allocation changed → fires",
			old:  makeHV(1024, 256, nil),
			new: func() hv1.Hypervisor {
				h := makeHV(1024, 256, nil)
				h.Status.Allocation = map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: gib(512)}
				return h
			}(),
			wantFire: true,
		},
		{
			name:     "nothing relevant changed → does not fire",
			old:      makeHV(1024, 256, nil),
			new:      makeHV(1024, 256, nil),
			wantFire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := tt.old
			newObj := tt.new
			got := hvCapacityChangePredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: &oldObj,
				ObjectNew: &newObj,
			})
			if got != tt.wantFire {
				t.Errorf("hvCapacityChangePredicate.UpdateFunc = %v, want %v", got, tt.wantFire)
			}
		})
	}
}

func TestReconcile_RevokesReadyWhenTargetHostCleared(t *testing.T) {
	scheme := newCRTestScheme(t)

	// Slot is Ready (Status.Host set, condition true) but Spec.TargetHost was cleared —
	// simulates the oversubscription eviction path.
	slot := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-1"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: "",
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ResourceName: "test-flavor",
				ProjectID:    "test-project",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: "host-1",
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReservationConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "ReservationActive",
					LastTransitionTime: metav1.Now(),
				},
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocation: map[string]v1alpha1.CommittedResourceAllocation{
					"vm-1": {ObjectRef: corev1.ObjectReference{Name: "vm-1"}},
				},
			},
		},
	}

	k8sClient := newCRTestClient(scheme, slot)
	controller := &CommitmentReservationController{
		Client: k8sClient,
		Conf:   ReservationControllerConfig{RequeueIntervalActive: metav1.Duration{Duration: 30 * time.Minute}},
	}

	_, err := controller.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "slot-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "slot-1"}, &updated); err != nil {
		t.Fatalf("failed to get updated reservation: %v", err)
	}
	if updated.Status.Host != "" {
		t.Errorf("expected Status.Host cleared, got %q", updated.Status.Host)
	}
	if updated.Status.CommittedResourceReservation != nil && len(updated.Status.CommittedResourceReservation.Allocation) != 0 {
		t.Errorf("expected Status.allocation cleared, got %v", updated.Status.CommittedResourceReservation.Allocation)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReservationConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("expected Ready=False, got %v", cond)
	}
	if cond != nil && cond.Reason != "PlacementRevoked" {
		t.Errorf("expected reason PlacementRevoked, got %q", cond.Reason)
	}
}

// ============================================================================
// Helpers for reconcileAllocations table test
// ============================================================================

// errForUUID is a VMSource that returns a simulated error for one specific UUID
// and delegates all other lookups to a backing vmSourceByUUID map.
type errForUUID struct {
	errUUID  string
	err      error
	fallback *vmSourceByUUID
}

func (e *errForUUID) GetVM(_ context.Context, uuid string) (*reservations.VM, error) {
	if uuid == e.errUUID {
		return nil, e.err
	}
	return e.fallback.GetVM(context.Background(), uuid)
}
func (e *errForUUID) ListVMs(_ context.Context) ([]reservations.VM, error) { return nil, nil }
func (e *errForUUID) ListVMsByProject(_ context.Context, _ string) ([]reservations.VM, error) {
	return nil, nil
}
func (e *errForUUID) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]reservations.VM, error) {
	return nil, nil
}
func (e *errForUUID) IsServerActive(_ context.Context, _ string) (bool, error) { return false, nil }
func (e *errForUUID) GetDeletedVMInfo(_ context.Context, _ string) (*reservations.DeletedVMInfo, error) {
	return nil, nil
}

// crSlot is a compact builder for CommittedResource reservation test objects.
func crSlot(name, host string, memGiB int64, annotations map[string]string) *v1alpha1.Reservation { //nolint:unparam
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: host,
			Resources:  map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(memGiB)},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:       "p-1",
				ResourceName:    "test-flavor",
				PrimaryResource: hv1.ResourceMemory,
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host:                         host,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{},
		},
	}
}

// withAlloc adds a confirmed allocation to a slot for the given VM.
func withAlloc(slot *v1alpha1.Reservation, vmUUID string, memGiB int64) *v1alpha1.Reservation {
	if slot.Status.CommittedResourceReservation.Allocation == nil {
		slot.Status.CommittedResourceReservation.Allocation = make(map[string]v1alpha1.CommittedResourceAllocation)
	}
	slot.Status.CommittedResourceReservation.Allocation[vmUUID] = v1alpha1.CommittedResourceAllocation{
		ObjectRef: corev1.ObjectReference{Name: vmUUID},
		Resources: map[hv1.ResourceName]resource.Quantity{hv1.ResourceMemory: testGiB(memGiB)},
	}
	return slot
}

// memVM builds a reservations.VM with the given memory on the given host.
func memVM(uuid, host string, memGiB int64) *reservations.VM {
	return &reservations.VM{
		UUID:              uuid,
		CurrentHypervisor: host,
		Resources:         map[string]resource.Quantity{string(hv1.ResourceMemory): testGiB(memGiB)},
	}
}

// getAlloc returns the memory allocation for vmUUID in a slot, or 0 if absent.
func getAllocGiB(slot *v1alpha1.Reservation, vmUUID string) resource.Quantity { //nolint:unparam
	if slot.Status.CommittedResourceReservation == nil {
		return testGiB(0)
	}
	alloc, ok := slot.Status.CommittedResourceReservation.Allocation[vmUUID]
	if !ok {
		return testGiB(0)
	}
	q, ok := alloc.Resources[hv1.ResourceMemory]
	if !ok {
		return testGiB(0)
	}
	return q
}

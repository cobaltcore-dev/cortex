// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

// Table-driven integration tests for the committed-resource lifecycle.
//
// Each test case wires CommittedResourceController and CommitmentReservationController
// against a shared fake k8s client and a mock Nova scheduler, then drives both
// controllers synchronously until every CR reaches a terminal condition.
//
// Terminal conditions (no further reconcile expected without external input):
//   - Ready=True  / Accepted
//   - Ready=False / Rejected
//   - Ready=False / Planned   (controller waits for StartTime)
//   - Ready=False / Expired   (controller has cleaned up children)
//   - Ready=False / Superseded

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Test cases
// ============================================================================

// CRIntegrationTestCase defines one end-to-end scenario for the committed-resource
// lifecycle spanning both controllers and the mock scheduler.
type CRIntegrationTestCase struct {
	Name string

	// Initial cluster state.
	Hypervisors          []*hv1.Hypervisor
	ExistingReservations []*v1alpha1.Reservation // pre-placed slots (for expiry/supersede scenarios)

	// CRs to create and drive to terminal state.
	CommittedResources []*v1alpha1.CommittedResource

	// When true the mock scheduler returns an empty hosts list (NoHostsFound).
	SchedulerRejects bool
	// SchedulerAcceptFirst, when > 0, makes the mock scheduler accept only the first N
	// placement calls and reject all subsequent ones. Used to test partial placement
	// (e.g. first slot placed, second slot rejected). Takes precedence over SchedulerRejects.
	SchedulerAcceptFirst int

	// Expected state after all CRs reach a terminal condition.
	ExpectedSlots int      // total Reservation CRDs remaining in the store
	AcceptedCRs   []string // CRs expected Ready=True / Accepted
	RejectedCRs   []string // CRs expected Ready=False / Rejected
	PlannedCRs    []string // CRs expected Ready=False / Planned
	ExpiredCRs    []string // CRs expected Ready=False / Expired
	SupersededCRs []string // CRs expected Ready=False / Superseded
}

func TestCRIntegration(t *testing.T) {
	testCases := []CRIntegrationTestCase{
		// ------------------------------------------------------------------
		// Acceptance: slot count from commitment amount
		// ------------------------------------------------------------------
		{
			Name: "single confirmed CR: one slot placed, CR accepted",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-1", "uuid-intg-0001", v1alpha1.CommitmentStatusConfirmed),
			},
			ExpectedSlots: 1,
			AcceptedCRs:   []string{"cr-1"},
		},
		{
			// 8 GiB commitment with the default 4 GiB test flavor → 2 slots
			Name: "large CR: commitment amount spans multiple flavors, two slots placed",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCRAmount("cr-large", "uuid-intg-0002", v1alpha1.CommitmentStatusConfirmed, "8Gi"),
			},
			ExpectedSlots: 2,
			AcceptedCRs:   []string{"cr-large"},
		},
		// ------------------------------------------------------------------
		// Pending / guaranteed: same placement path as confirmed
		// ------------------------------------------------------------------
		{
			Name: "pending CR: slot placed, CR accepted",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-pending", "uuid-intg-0003", v1alpha1.CommitmentStatusPending),
			},
			ExpectedSlots: 1,
			AcceptedCRs:   []string{"cr-pending"},
		},
		{
			Name: "guaranteed CR: slot placed, CR accepted",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-guaranteed", "uuid-intg-0004", v1alpha1.CommitmentStatusGuaranteed),
			},
			ExpectedSlots: 1,
			AcceptedCRs:   []string{"cr-guaranteed"},
		},
		// ------------------------------------------------------------------
		// Planned: no slots, condition stays Planned
		// ------------------------------------------------------------------
		{
			Name: "planned CR: no slots created, condition stays Planned",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-planned", "uuid-intg-0005", v1alpha1.CommitmentStatusPlanned),
			},
			ExpectedSlots: 0,
			PlannedCRs:    []string{"cr-planned"},
		},
		// ------------------------------------------------------------------
		// Rejection paths
		// ------------------------------------------------------------------
		{
			Name: "scheduler returns no hosts: CR rejected and slots cleaned up",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCRAllowRejection("cr-rej", "uuid-intg-0006", v1alpha1.CommitmentStatusConfirmed),
			},
			SchedulerRejects: true,
			ExpectedSlots:    0,
			RejectedCRs:      []string{"cr-rej"},
		},
		{
			// Reservation controller detects the empty hosts list before calling the scheduler.
			Name:        "no hypervisors in cluster: CR rejected with NoHostsAvailable",
			Hypervisors: []*hv1.Hypervisor{},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCRAllowRejection("cr-nohosts", "uuid-intg-0007", v1alpha1.CommitmentStatusConfirmed),
			},
			ExpectedSlots: 0,
			RejectedCRs:   []string{"cr-nohosts"},
		},
		// ------------------------------------------------------------------
		// Multiple independent CRs
		// ------------------------------------------------------------------
		{
			Name: "two CRs with different UUIDs: each gets its own slot, both accepted",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
				intgHypervisor("host-2"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-a", "uuid-intg-0008", v1alpha1.CommitmentStatusConfirmed),
				intgCR("cr-b", "uuid-intg-0009", v1alpha1.CommitmentStatusConfirmed),
			},
			ExpectedSlots: 2,
			AcceptedCRs:   []string{"cr-a", "cr-b"},
		},
		{
			// One CR in planned state should not block the other from being accepted.
			Name: "one planned CR and one confirmed CR: only confirmed CR gets a slot",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-plan", "uuid-intg-0010", v1alpha1.CommitmentStatusPlanned),
				intgCR("cr-conf", "uuid-intg-0011", v1alpha1.CommitmentStatusConfirmed),
			},
			ExpectedSlots: 1,
			PlannedCRs:    []string{"cr-plan"},
			AcceptedCRs:   []string{"cr-conf"},
		},
		// ------------------------------------------------------------------
		// Inactive states: existing slots must be cleaned up
		// ------------------------------------------------------------------
		{
			Name: "expired CR with existing slot: slot deleted, CR marked inactive",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			ExistingReservations: []*v1alpha1.Reservation{
				intgExistingReservation("cr-expire-0", "uuid-intg-0012"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-expire", "uuid-intg-0012", v1alpha1.CommitmentStatusExpired),
			},
			ExpectedSlots: 0,
			ExpiredCRs:    []string{"cr-expire"},
		},
		{
			Name: "superseded CR with existing slot: slot deleted, CR marked inactive",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			ExistingReservations: []*v1alpha1.Reservation{
				intgExistingReservation("cr-supersede-0", "uuid-intg-0013"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCR("cr-supersede", "uuid-intg-0013", v1alpha1.CommitmentStatusSuperseded),
			},
			ExpectedSlots: 0,
			SupersededCRs: []string{"cr-supersede"},
		},
		// ------------------------------------------------------------------
		// Spec validation: unknown flavor group
		// ------------------------------------------------------------------
		{
			// ApplyCommitmentState returns "flavor group not found" which triggers
			// rollback+Rejected (AllowRejection=true); no child slots are ever created.
			Name: "unknown flavor group: CR rejected, no slots created",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCRUnknownFlavorGroup("cr-unk", "uuid-intg-0014", v1alpha1.CommitmentStatusConfirmed),
			},
			ExpectedSlots: 0,
			RejectedCRs:   []string{"cr-unk"},
		},
		// ------------------------------------------------------------------
		// Partial placement: first slot placed, second slot rejected
		// ------------------------------------------------------------------
		{
			// 8 GiB CR needs 2 slots. Scheduler accepts the first call (slot 0 placed)
			// then rejects the second (slot 1 gets NoHostsFound). With AllowRejection=true
			// the CR controller rolls back: deletes both slots and sets Rejected.
			Name: "partial placement: first slot placed, second slot rejected, CR rolled back",
			Hypervisors: []*hv1.Hypervisor{
				intgHypervisor("host-1"),
			},
			CommittedResources: []*v1alpha1.CommittedResource{
				intgCRAmountAllowRejection("cr-partial", "uuid-intg-0015", v1alpha1.CommitmentStatusConfirmed, "8Gi"),
			},
			SchedulerAcceptFirst: 1,
			ExpectedSlots:        0,
			RejectedCRs:          []string{"cr-partial"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runCRIntegrationTestCase(t, tc)
		})
	}
}

// ============================================================================
// Runner
// ============================================================================

func runCRIntegrationTestCase(t *testing.T, tc CRIntegrationTestCase) {
	t.Helper()

	schedulerFn := intgAcceptScheduler
	switch {
	case tc.SchedulerAcceptFirst > 0:
		schedulerFn = intgAcceptFirstScheduler(tc.SchedulerAcceptFirst)
	case tc.SchedulerRejects:
		schedulerFn = intgRejectScheduler
	}

	objects := []client.Object{newTestFlavorKnowledge()}
	for _, hv := range tc.Hypervisors {
		objects = append(objects, hv)
	}
	for _, res := range tc.ExistingReservations {
		objects = append(objects, res)
	}

	env := newIntgEnv(t, objects, schedulerFn)
	defer env.close()

	crNames := make([]string, len(tc.CommittedResources))
	for i, cr := range tc.CommittedResources {
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR %s: %v", cr.Name, err)
		}
		crNames[i] = cr.Name
	}

	intgDriveToTerminal(t, env, crNames)

	// Assert total reservation slot count.
	var resList v1alpha1.ReservationList
	if err := env.k8sClient.List(context.Background(), &resList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(resList.Items) != tc.ExpectedSlots {
		t.Errorf("reservation slots: want %d, got %d", tc.ExpectedSlots, len(resList.Items))
	}

	// Assert CR conditions.
	intgAssertCRCondition(t, env.k8sClient, tc.AcceptedCRs, metav1.ConditionTrue, v1alpha1.CommittedResourceReasonAccepted)
	intgAssertCRCondition(t, env.k8sClient, tc.RejectedCRs, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)
	intgAssertCRCondition(t, env.k8sClient, tc.PlannedCRs, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonPlanned)
	intgAssertCRCondition(t, env.k8sClient, tc.ExpiredCRs, metav1.ConditionFalse, string(v1alpha1.CommitmentStatusExpired))
	intgAssertCRCondition(t, env.k8sClient, tc.SupersededCRs, metav1.ConditionFalse, string(v1alpha1.CommitmentStatusSuperseded))
}

// ============================================================================
// Integration environment
// ============================================================================

type intgEnv struct {
	k8sClient     client.Client
	crController  *CommittedResourceController
	resController *CommitmentReservationController
	schedulerSrv  *httptest.Server
}

func newIntgEnv(t *testing.T, initialObjects []client.Object, schedulerFn http.HandlerFunc) *intgEnv {
	t.Helper()
	scheme := newCRTestScheme(t)

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initialObjects...).
		WithStatusSubresource(
			&v1alpha1.CommittedResource{},
			&v1alpha1.Reservation{},
			&v1alpha1.Knowledge{},
		).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByCommitmentUUID, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.CommitmentUUID == "" {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, idxCommittedResourceByUUID, func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.CommitmentUUID == "" {
				return nil
			}
			return []string{cr.Spec.CommitmentUUID}
		}).
		Build()

	schedulerSrv := httptest.NewServer(schedulerFn)

	crCtrl := &CommittedResourceController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: 5 * time.Minute}},
	}
	resCtrl := &CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf: ReservationControllerConfig{
			SchedulerURL:          schedulerSrv.URL,
			AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute},
			RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
		},
	}
	if err := resCtrl.Init(context.Background(), resCtrl.Conf); err != nil {
		t.Fatalf("resCtrl.Init: %v", err)
	}
	return &intgEnv{k8sClient: k8sClient, crController: crCtrl, resController: resCtrl, schedulerSrv: schedulerSrv}
}

func (e *intgEnv) close() { e.schedulerSrv.Close() }

// ============================================================================
// Reconcile driver
// ============================================================================

// intgDriveToTerminal runs reconcile passes until every named CR has a terminal
// condition or the 5 s deadline is reached.
//
// One pass:
//  1. CR controller (adds finalizer / creates Reservation CRDs / handles inactive states)
//  2. Reservation controller ×2 per slot (first call sets TargetHost, second sets Ready=True)
//  3. CR controller again (picks up placement outcomes: Accepted or Rejected)
func intgDriveToTerminal(t *testing.T, env *intgEnv, crNames []string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)

	for {
		if time.Now().After(deadline) {
			for _, name := range crNames {
				var cr v1alpha1.CommittedResource
				if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: name}, &cr); err == nil {
					t.Logf("CR %s: conditions=%v", name, cr.Status.Conditions)
				}
			}
			t.Fatal("timed out waiting for CRs to reach terminal state")
		}

		allDone := true
		for _, name := range crNames {
			var cr v1alpha1.CommittedResource
			if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
				continue // deleted = done
			}
			if !intgIsTerminalCR(cr) {
				allDone = false
			}
		}
		if allDone {
			return
		}

		// Pass 1: CR controller.
		for _, name := range crNames {
			var cr v1alpha1.CommittedResource
			if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
				continue
			}
			if intgIsTerminalCR(cr) {
				continue
			}
			env.crController.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}) //nolint:errcheck
		}

		// Pass 2: Reservation controller (two reconciles per slot).
		var resList v1alpha1.ReservationList
		env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
			v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
		})
		for _, res := range resList.Items {
			if intgIsTerminalReservation(res) {
				continue
			}
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
			env.resController.Reconcile(ctx, req) //nolint:errcheck
			env.resController.Reconcile(ctx, req) //nolint:errcheck
		}

		// Pass 3: CR controller picks up Reservation outcomes.
		for _, name := range crNames {
			var cr v1alpha1.CommittedResource
			if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
				continue
			}
			if intgIsTerminalCR(cr) {
				continue
			}
			env.crController.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}) //nolint:errcheck
		}
	}
}

func intgIsTerminalCR(cr v1alpha1.CommittedResource) bool {
	if !cr.DeletionTimestamp.IsZero() {
		return false // needs one more reconcile to remove its finalizer
	}
	cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil {
		return false
	}
	if cond.Status == metav1.ConditionTrue {
		return true
	}
	return cond.Reason == v1alpha1.CommittedResourceReasonRejected ||
		cond.Reason == v1alpha1.CommittedResourceReasonPlanned ||
		cond.Reason == string(v1alpha1.CommitmentStatusExpired) ||
		cond.Reason == string(v1alpha1.CommitmentStatusSuperseded)
}

// intgIsTerminalReservation returns true once the Reservation controller has set any
// condition (Ready=True after placement, or Ready=False after rejection).
func intgIsTerminalReservation(res v1alpha1.Reservation) bool {
	return meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady) != nil
}

// ============================================================================
// Assertion helpers
// ============================================================================

func intgAssertCRCondition(t *testing.T, k8sClient client.Client, crNames []string, wantStatus metav1.ConditionStatus, wantReason string) {
	t.Helper()
	for _, name := range crNames {
		var cr v1alpha1.CommittedResource
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &cr); err != nil {
			t.Errorf("CR %s not found: %v", name, err)
			continue
		}
		cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			t.Errorf("CR %s: no Ready condition", name)
			continue
		}
		if cond.Status != wantStatus || cond.Reason != wantReason {
			t.Errorf("CR %s: want Ready=%s/Reason=%s, got Ready=%s/Reason=%s", name, wantStatus, wantReason, cond.Status, cond.Reason)
		}
	}
}

// ============================================================================
// Scheduler handlers
// ============================================================================

func intgAcceptScheduler(w http.ResponseWriter, r *http.Request) {
	resp := &schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{"host-1"}}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func intgRejectScheduler(w http.ResponseWriter, r *http.Request) {
	resp := &schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{}}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// intgAcceptFirstScheduler returns a handler that accepts the first count placement calls
// and rejects all subsequent ones. Uses an atomic counter so concurrent calls are safe.
func intgAcceptFirstScheduler(count int) http.HandlerFunc {
	var calls atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if int(calls.Add(1)) <= count {
			intgAcceptScheduler(w, r)
		} else {
			intgRejectScheduler(w, r)
		}
	}
}

// intgRejectFirstScheduler returns a handler that rejects the first count placement calls
// and accepts all subsequent ones. Used to test AllowRejection=false retry-until-success paths.
func intgRejectFirstScheduler(count int) http.HandlerFunc {
	var calls atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if int(calls.Add(1)) <= count {
			intgRejectScheduler(w, r)
		} else {
			intgAcceptScheduler(w, r)
		}
	}
}

// ============================================================================
// Test object builders
// ============================================================================

// intgHypervisor returns a minimal Hypervisor with the given name.
func intgHypervisor(name string) *hv1.Hypervisor {
	return &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// intgCR returns a CommittedResource with the default 4 GiB amount.
// commitmentUUID must be unique per test case to avoid field-index collisions.
func intgCR(name, commitmentUUID string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	cr := newTestCommittedResource(name, state)
	cr.Spec.CommitmentUUID = commitmentUUID
	return cr
}

// intgCRAmount returns a CommittedResource with a custom amount string (e.g. "8Gi").
func intgCRAmount(name, commitmentUUID string, state v1alpha1.CommitmentStatus, amount string) *v1alpha1.CommittedResource {
	cr := intgCR(name, commitmentUUID, state)
	cr.Spec.Amount = resource.MustParse(amount)
	return cr
}

// intgCRAllowRejection returns a CommittedResource with AllowRejection=true so the
// controller rolls back and sets Rejected (rather than retrying indefinitely).
func intgCRAllowRejection(name, commitmentUUID string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	cr := intgCR(name, commitmentUUID, state)
	cr.Spec.AllowRejection = true
	return cr
}

// intgCRAmountAllowRejection returns a CommittedResource with a custom amount and AllowRejection=true.
func intgCRAmountAllowRejection(name, commitmentUUID string, state v1alpha1.CommitmentStatus, amount string) *v1alpha1.CommittedResource {
	cr := intgCRAmount(name, commitmentUUID, state, amount)
	cr.Spec.AllowRejection = true
	return cr
}

// intgCRUnknownFlavorGroup returns a CommittedResource referencing a flavor group
// that does not exist in the Knowledge CRD, with AllowRejection=true so the
// controller reaches Rejected rather than retrying indefinitely.
func intgCRUnknownFlavorGroup(name, commitmentUUID string, state v1alpha1.CommitmentStatus) *v1alpha1.CommittedResource {
	cr := intgCRAllowRejection(name, commitmentUUID, state)
	cr.Spec.FlavorGroupName = "nonexistent-group"
	return cr
}

// intgExistingReservation returns a pre-placed Reservation tied to the given commitment UUID,
// used to verify that expiry/supersede paths delete children.
func intgExistingReservation(name, commitmentUUID string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: commitmentUUID,
			},
		},
	}
}

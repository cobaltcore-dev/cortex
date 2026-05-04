// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

// Integration tests for the committed-resource lifecycle.
//
// Both test suites wire CommittedResourceController and CommitmentReservationController
// against a shared fake k8s client and a mock Nova scheduler:
//   - TestCRIntegration  — table-driven declarative scenarios
//   - TestCRLifecycle    — imperative sub-tests for multi-step transitions
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
	"strings"
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

func newDefaultIntgEnv(t *testing.T) *intgEnv {
	t.Helper()
	objects := []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}
	return newIntgEnv(t, objects, intgAcceptScheduler)
}

func (e *intgEnv) reconcileCR(t *testing.T, crName string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: crName}}
	if _, err := e.crController.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("CR reconcile: %v", err)
	}
}

func (e *intgEnv) reconcileReservation(t *testing.T, resName string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: resName}}
	if _, err := e.resController.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reservation reconcile %s: %v", resName, err)
	}
}

func (e *intgEnv) listChildReservations(t *testing.T, crName string) []v1alpha1.Reservation {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := e.k8sClient.List(context.Background(), &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	prefix := crName + "-"
	var children []v1alpha1.Reservation
	for _, r := range list.Items {
		if strings.HasPrefix(r.Name, prefix) {
			children = append(children, r)
		}
	}
	return children
}

func (e *intgEnv) getCR(t *testing.T, name string) v1alpha1.CommittedResource {
	t.Helper()
	var cr v1alpha1.CommittedResource
	if err := e.k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &cr); err != nil {
		t.Fatalf("get CR %s: %v", name, err)
	}
	return cr
}

// reconcileChildReservations runs the reservation controller twice on every child Reservation
// for crName (first reconcile sets TargetHost, second sets Ready=True), then re-reconciles
// the CR so it can observe the placement outcomes.
func (e *intgEnv) reconcileChildReservations(t *testing.T, crName string) {
	t.Helper()
	for _, res := range e.listChildReservations(t, crName) {
		e.reconcileReservation(t, res.Name) // calls scheduler → sets TargetHost
		e.reconcileReservation(t, res.Name) // syncs TargetHost to Status → Ready=True
	}
	e.reconcileCR(t, crName)
}

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

// ============================================================================
// Imperative lifecycle tests
// ============================================================================

// TestCRLifecycle covers multi-step state transitions that require imperative
// mid-test patches and cannot be expressed as a purely declarative table.
func TestCRLifecycle(t *testing.T) {
	t.Run("planned→confirmed: child Reservations created and placed", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusPlanned)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Reconcile as planned: finalizer added, no Reservations.
		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Fatalf("planned: expected 0 reservations, got %d", len(got))
		}
		crState := env.getCR(t, cr.Name)
		cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.Reason != "Planned" {
			t.Errorf("planned: expected Reason=Planned, got %v", cond)
		}

		// Transition to confirmed.
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.State = v1alpha1.CommitmentStatusConfirmed
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch state to confirmed: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		children := env.listChildReservations(t, cr.Name)
		if len(children) != 1 {
			t.Fatalf("confirmed: expected 1 reservation, got %d", len(children))
		}
		env.reconcileChildReservations(t, cr.Name)

		crState = env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("confirmed: expected Ready=True")
		}
	})

	t.Run("confirmed→expired: child Reservations deleted, CR marked inactive", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Bring to confirmed+Ready=True.
		env.reconcileCR(t, cr.Name)                // adds finalizer
		env.reconcileCR(t, cr.Name)                // creates Reservations
		env.reconcileChildReservations(t, cr.Name) // places slots → Ready=True

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Fatalf("pre-expire: expected 1 reservation, got %d", len(got))
		}

		// Transition to expired.
		crState := env.getCR(t, cr.Name)
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.State = v1alpha1.CommitmentStatusExpired
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch state to expired: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Errorf("expired: expected 0 reservations, got %d", len(got))
		}
		crState = env.getCR(t, cr.Name)
		cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Errorf("expired: expected Ready=False, got %v", cond)
		}
		if cond != nil && cond.Reason != string(v1alpha1.CommitmentStatusExpired) {
			t.Errorf("expired: expected Reason=%s, got %s", v1alpha1.CommitmentStatusExpired, cond.Reason)
		}
	})

	t.Run("reservation placement: two reconciles set TargetHost then Ready=True", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)

		children := env.listChildReservations(t, cr.Name)
		if len(children) != 1 {
			t.Fatalf("expected 1 child reservation, got %d", len(children))
		}
		child := children[0]

		// First reconcile: scheduler call → TargetHost written to Spec.
		env.reconcileReservation(t, child.Name)
		var afterFirst v1alpha1.Reservation
		if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: child.Name}, &afterFirst); err != nil {
			t.Fatalf("get reservation after first reconcile: %v", err)
		}
		if afterFirst.Spec.TargetHost == "" {
			t.Fatalf("expected TargetHost set after first reservation reconcile")
		}

		// Second reconcile: TargetHost synced to Status, Ready=True.
		env.reconcileReservation(t, child.Name)
		var afterSecond v1alpha1.Reservation
		if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: child.Name}, &afterSecond); err != nil {
			t.Fatalf("get reservation after second reconcile: %v", err)
		}
		if !meta.IsStatusConditionTrue(afterSecond.Status.Conditions, v1alpha1.ReservationConditionReady) {
			t.Errorf("expected reservation Ready=True after placement, got %v", afterSecond.Status.Conditions)
		}
		if afterSecond.Status.Host != "host-1" {
			t.Errorf("expected Status.Host=host-1, got %q", afterSecond.Status.Host)
		}
	})

	t.Run("deletion: finalizer removed, child Reservations cleaned up", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Pre-create a child Reservation to verify it gets cleaned up on deletion.
		// newTestCommittedResource pre-populates the finalizer, so Delete() immediately sets DeletionTimestamp.
		child := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cr-0",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeCommittedResource,
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					CommitmentUUID: "test-uuid-1234",
				},
			},
		}
		if err := env.k8sClient.Create(context.Background(), child); err != nil {
			t.Fatalf("create child reservation: %v", err)
		}

		crState := env.getCR(t, cr.Name)
		if err := env.k8sClient.Delete(context.Background(), &crState); err != nil {
			t.Fatalf("delete CR: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Errorf("post-deletion: expected 0 reservations, got %d", len(got))
		}
		var final v1alpha1.CommittedResource
		err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &final)
		if client.IgnoreNotFound(err) != nil {
			t.Fatalf("unexpected error after deletion: %v", err)
		}
		if err == nil {
			for _, f := range final.Finalizers {
				if f == crFinalizer {
					t.Errorf("finalizer not removed after deletion reconcile")
				}
			}
		}
	})

	t.Run("confirmed→superseded: child Reservations deleted, CR marked inactive", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Fatalf("pre-supersede: expected 1 reservation, got %d", len(got))
		}

		crState := env.getCR(t, cr.Name)
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.State = v1alpha1.CommitmentStatusSuperseded
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch state to superseded: %v", err)
		}
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 0 {
			t.Errorf("superseded: expected 0 reservations, got %d", len(got))
		}
		crState = env.getCR(t, cr.Name)
		cond := meta.FindStatusCondition(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Errorf("superseded: expected Ready=False, got %v", cond)
		}
		if cond != nil && cond.Reason != string(v1alpha1.CommitmentStatusSuperseded) {
			t.Errorf("superseded: expected Reason=%s, got %s", v1alpha1.CommitmentStatusSuperseded, cond.Reason)
		}
	})

	t.Run("idempotency: extra reconciles after Accepted do not create extra slots", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Fatalf("pre-idempotency check: expected 1 reservation, got %d", len(got))
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Errorf("idempotency: expected 1 reservation after extra reconciles, got %d", len(got))
		}
		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("idempotency: expected CR to remain Ready=True after extra reconciles")
		}
	})

	t.Run("AllowRejection=false: stays Reserving when scheduler rejects", func(t *testing.T) {
		env := newIntgEnv(t, []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}, intgRejectScheduler)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		// AllowRejection stays false (the default), so placement failure must requeue, not reject.
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
		for range 3 {
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
			var resList v1alpha1.ReservationList
			env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			})
			for _, res := range resList.Items {
				resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			}
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		}

		var final v1alpha1.CommittedResource
		if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, &final); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		cond := meta.FindStatusCondition(final.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			t.Fatalf("no Ready condition")
		}
		if cond.Reason == v1alpha1.CommittedResourceReasonRejected {
			t.Errorf("AllowRejection=false: CR must not transition to Rejected, got Reason=%s", cond.Reason)
		}
		if cond.Reason != v1alpha1.CommittedResourceReasonReserving {
			t.Errorf("AllowRejection=false: expected Reason=Reserving, got %s", cond.Reason)
		}
	})

	t.Run("externally deleted child Reservation is recreated by CR controller", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		children := env.listChildReservations(t, cr.Name)
		if len(children) != 1 {
			t.Fatalf("expected 1 child reservation before deletion, got %d", len(children))
		}

		// Simulate out-of-band deletion of the slot.
		child := children[0]
		if err := env.k8sClient.Delete(context.Background(), &child); err != nil {
			t.Fatalf("delete child reservation: %v", err)
		}

		// CR controller detects the missing slot and recreates it.
		env.reconcileCR(t, cr.Name)
		// Place the new slot.
		env.reconcileChildReservations(t, cr.Name)
		// CR controller observes Ready=True on the recreated slot.
		env.reconcileCR(t, cr.Name)

		if got := env.listChildReservations(t, cr.Name); len(got) != 1 {
			t.Errorf("expected 1 reservation after recreation, got %d", len(got))
		}
		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Errorf("expected CR to be Ready=True after slot recreation")
		}
	})

	t.Run("AcceptedAt: set when CR accepted", func(t *testing.T) {
		env := newDefaultIntgEnv(t)
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		env.reconcileCR(t, cr.Name)
		env.reconcileCR(t, cr.Name)
		env.reconcileChildReservations(t, cr.Name)

		crState := env.getCR(t, cr.Name)
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Fatalf("expected CR to be Ready=True")
		}
		if crState.Status.AcceptedAt == nil {
			t.Errorf("expected AcceptedAt to be set on acceptance")
		}
		if crState.Status.AcceptedAmount == nil {
			t.Errorf("expected AcceptedAmount to be set on acceptance")
		} else if crState.Status.AcceptedAmount.Cmp(resource.MustParse("4Gi")) != 0 {
			t.Errorf("AcceptedAmount: want 4Gi, got %s", crState.Status.AcceptedAmount.String())
		}
	})

	t.Run("resize failure: rolls back to AcceptedAmount, prior slot preserved", func(t *testing.T) {
		// Scheduler: accepts the first placement call (initial 4 GiB slot), rejects all subsequent.
		objects := []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}
		env := newIntgEnv(t, objects, intgAcceptFirstScheduler(1))
		defer env.close()

		cr := intgCRAllowRejection("my-cr", "uuid-resize-0001", v1alpha1.CommitmentStatusConfirmed)
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		// Phase 1: accept at 4 GiB (1 slot). Uses 1 scheduler call.
		intgDriveToTerminal(t, env, []string{cr.Name})
		var crState v1alpha1.CommittedResource
		if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: cr.Name}, &crState); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		if !meta.IsStatusConditionTrue(crState.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
			t.Fatalf("phase 1: expected CR to be Ready=True after initial placement")
		}
		if crState.Status.AcceptedAmount == nil || crState.Status.AcceptedAmount.Cmp(resource.MustParse("4Gi")) != 0 {
			t.Fatalf("phase 1: AcceptedAmount must be 4Gi, got %v", crState.Status.AcceptedAmount)
		}

		// Phase 2: resize to 8 GiB (needs 2 slots). Scheduler has no more accepts.
		patch := client.MergeFrom(crState.DeepCopy())
		crState.Spec.Amount = resource.MustParse("8Gi")
		if err := env.k8sClient.Patch(context.Background(), &crState, patch); err != nil {
			t.Fatalf("patch CR to 8Gi: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}

		// CR controller: applyReservationState bumps gen on existing slot, creates 2nd slot.
		env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		// Reservation controller: existing slot echoes new ParentGeneration (no scheduler call);
		// new slot calls scheduler → rejected.
		var resList v1alpha1.ReservationList
		env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
			v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
		})
		for _, res := range resList.Items {
			resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
			env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			env.resController.Reconcile(ctx, resReq) //nolint:errcheck
		}
		// CR controller: detects 2nd slot Ready=False → rollbackToAccepted (keeps 1 slot) → Rejected.
		env.crController.Reconcile(ctx, crReq) //nolint:errcheck

		// Rollback must preserve 1 slot (matching AcceptedAmount=4Gi), not delete all.
		var finalList v1alpha1.ReservationList
		if err := env.k8sClient.List(ctx, &finalList, client.MatchingLabels{
			v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
		}); err != nil {
			t.Fatalf("list reservations: %v", err)
		}
		if len(finalList.Items) != 1 {
			t.Errorf("resize rollback: want 1 slot (AcceptedAmount), got %d", len(finalList.Items))
		}
		intgAssertCRCondition(t, env.k8sClient, []string{cr.Name}, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected)
	})

	t.Run("AllowRejection=false: eventually accepted after scheduler starts accepting", func(t *testing.T) {
		// Scheduler rejects the first 2 calls (one per reservation controller reconcile pair),
		// then accepts all subsequent. AllowRejection=false means the CR controller retries rather
		// than rejecting, so the CR must eventually reach Accepted once the scheduler cooperates.
		objects := []client.Object{newTestFlavorKnowledge(), intgHypervisor("host-1")}
		env := newIntgEnv(t, objects, intgRejectFirstScheduler(2))
		defer env.close()

		cr := newTestCommittedResource("my-cr", v1alpha1.CommitmentStatusConfirmed)
		// AllowRejection stays false (default), so placement failure must requeue, not reject.
		if err := env.k8sClient.Create(context.Background(), cr); err != nil {
			t.Fatalf("create CR: %v", err)
		}

		ctx := context.Background()
		crReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
		for range 3 {
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
			var resList v1alpha1.ReservationList
			env.k8sClient.List(ctx, &resList, client.MatchingLabels{ //nolint:errcheck
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			})
			for _, res := range resList.Items {
				resReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
				env.resController.Reconcile(ctx, resReq) //nolint:errcheck
			}
			env.crController.Reconcile(ctx, crReq) //nolint:errcheck
		}

		var final v1alpha1.CommittedResource
		if err := env.k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, &final); err != nil {
			t.Fatalf("get CR: %v", err)
		}
		cond := meta.FindStatusCondition(final.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			t.Fatalf("no Ready condition after retries")
		}
		if cond.Reason == v1alpha1.CommittedResourceReasonRejected {
			t.Errorf("AllowRejection=false: CR must not be Rejected, got Reason=%s", cond.Reason)
		}
		if cond.Status != metav1.ConditionTrue || cond.Reason != v1alpha1.CommittedResourceReasonAccepted {
			t.Errorf("AllowRejection=false: expected Ready=True/Accepted after retries, got Ready=%s/Reason=%s", cond.Status, cond.Reason)
		}
	})
}

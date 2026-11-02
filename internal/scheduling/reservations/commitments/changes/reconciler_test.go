// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package changes

import (
	"context"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ============================================================================
// Helpers
// ============================================================================

var testCtx = context.Background()

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func testClient(t *testing.T, scheme *runtime.Scheme, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(
			&v1alpha1.CommittedResourceChange{},
			&v1alpha1.CommittedResource{},
		).
		WithIndex(&v1alpha1.CommittedResourceChange{}, idxCRCByUpsertedCR, func(obj client.Object) []string {
			tx, ok := obj.(*v1alpha1.CommittedResourceChange)
			if !ok {
				return nil
			}
			names := make([]string, 0, len(tx.Status.UpsertedCommitments))
			for _, ref := range tx.Status.UpsertedCommitments {
				if ref.Name != "" {
					names = append(names, ref.Name)
				}
			}
			return names
		}).
		Build()
}

func testReconciler(c client.Client, scheme *runtime.Scheme) *CommittedResourceChangeReconciler {
	return &CommittedResourceChangeReconciler{Client: c, Scheme: scheme}
}

func doReconcile(t *testing.T, r *CommittedResourceChangeReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(testCtx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("Reconcile(%q): %v", name, err)
	}
	return res
}

func getTx(t *testing.T, c client.Client, name string) *v1alpha1.CommittedResourceChange {
	t.Helper()
	var tx v1alpha1.CommittedResourceChange
	if err := c.Get(testCtx, types.NamespacedName{Name: name}, &tx); err != nil {
		t.Fatalf("get CRC %q: %v", name, err)
	}
	return &tx
}

func getCR(t *testing.T, c client.Client, name string) *v1alpha1.CommittedResource {
	t.Helper()
	var cr v1alpha1.CommittedResource
	if err := c.Get(testCtx, types.NamespacedName{Name: name}, &cr); err != nil {
		t.Fatalf("get CR %q: %v", name, err)
	}
	return &cr
}

// driveToPhase calls Reconcile until the CRT reaches target, up to 5 attempts.
func driveToPhase(t *testing.T, r *CommittedResourceChangeReconciler, c client.Client, txName string, target v1alpha1.ChangePhase) {
	t.Helper()
	const maxSteps = 5
	for range maxSteps {
		if getTx(t, c, txName).Status.Phase == target {
			return
		}
		doReconcile(t, r, txName)
	}
	if got := getTx(t, c, txName).Status.Phase; got != target {
		t.Fatalf("driveToPhase: wanted %q, got %q after %d steps", target, got, maxSteps)
	}
}

// setCRReady simulates the CR controller writing a Ready condition using the CR's current Generation.
//

func setCRReady(t *testing.T, c client.Client, crName string, status metav1.ConditionStatus, reason, message string) {
	t.Helper()
	cr := getCR(t, c, crName)
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	})
	if err := c.Status().Update(testCtx, cr); err != nil {
		t.Fatalf("update CR %q status: %v", crName, err)
	}
}

// setCRReadyStaleGen sets Ready=True/Accepted but with a stale ObservedGeneration (generation-1),
// so the reconciler's generation gate treats the condition as not yet caught up.
func setCRReadyStaleGen(t *testing.T, c client.Client, crName string) {
	t.Helper()
	cr := getCR(t, c, crName)
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             v1alpha1.CommittedResourceReasonAccepted,
		ObservedGeneration: cr.Generation - 1,
		LastTransitionTime: metav1.Now(),
	})
	if err := c.Status().Update(testCtx, cr); err != nil {
		t.Fatalf("update CR %q status (stale gen): %v", crName, err)
	}
}

// backdateProgressingCondition sets the Progressing condition's LastTransitionTime to the past.
func backdateProgressingCondition(t *testing.T, c client.Client, txName string, age time.Duration) {
	t.Helper()
	tx := getTx(t, c, txName)
	for i, cond := range tx.Status.Conditions {
		if cond.Type == v1alpha1.ChangeConditionProgressing {
			tx.Status.Conditions[i].LastTransitionTime = metav1.NewTime(time.Now().Add(-age))
			break
		}
	}
	if err := c.Status().Update(testCtx, tx); err != nil {
		t.Fatalf("backdate Progressing condition on %q: %v", txName, err)
	}
}

// assertCondition verifies a condition in the given list by type, status, and optional reason.
// Pass an empty wantReason to skip the reason check.
func assertCondition(t *testing.T, conditions []metav1.Condition, condType string, wantStatus metav1.ConditionStatus, wantReason string) {
	t.Helper()
	cond := apimeta.FindStatusCondition(conditions, condType)
	if cond == nil {
		t.Errorf("condition %q not found in %v", condType, conditions)
		return
	}
	if cond.Status != wantStatus {
		t.Errorf("condition %q status: want %v, got %v", condType, wantStatus, cond.Status)
	}
	if wantReason != "" && cond.Reason != wantReason {
		t.Errorf("condition %q reason: want %q, got %q", condType, wantReason, cond.Reason)
	}
}

// minimalCRSpec returns a minimal valid CommittedResourceSpec for testing.
func minimalCRSpec(az string) *v1alpha1.CommittedResourceSpec {
	return &v1alpha1.CommittedResourceSpec{
		CommitmentUUID:   "test-uuid",
		FlavorGroupName:  "kvm_v2",
		ResourceType:     v1alpha1.CommittedResourceTypeMemory,
		Amount:           resource.MustParse("1Gi"),
		AvailabilityZone: az,
		ProjectID:        "project-1",
		DomainID:         "domain-1",
		State:            v1alpha1.CommitmentStatusConfirmed,
	}
}

// ============================================================================
// TestCRTReconciler_Preparing
// ============================================================================

func TestCRTReconciler_Preparing(t *testing.T) {
	tests := []struct {
		name                  string
		preObjects            []client.Object
		tx                    *v1alpha1.CommittedResourceChange
		wantPhase             v1alpha1.ChangePhase
		wantSnapshotNilFor    []string // UUIDs that must have nil snapshot entry (CR did not exist)
		wantSnapshotSetFor    []string // UUIDs that must have non-nil snapshot entry (CR existed)
		wantProgressingStatus metav1.ConditionStatus
		wantProgressingReason string
	}{
		{
			name: "no pre-existing CR: snapshot entry is nil, phase advances to Allocating",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-prep-1"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-a": minimalCRSpec("az-1")},
				},
			},
			wantPhase:             v1alpha1.ChangePhaseAllocating,
			wantSnapshotNilFor:    []string{"uuid-a"},
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "pre-existing CR spec is captured in SnapshotBefore",
			preObjects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-b"},
					Spec:       *minimalCRSpec("az-1"),
				},
			},
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-prep-2"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-b": minimalCRSpec("az-1")},
				},
			},
			wantPhase:             v1alpha1.ChangePhaseAllocating,
			wantSnapshotSetFor:    []string{"uuid-b"},
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "Preparing idempotent: running reconcile twice does not error",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-prep-3"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-c": minimalCRSpec("az-1")},
				},
			},
			wantPhase:             v1alpha1.ChangePhaseAllocating,
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			objs := append([]client.Object{tt.tx}, tt.preObjects...)
			c := testClient(t, scheme, objs...)
			r := testReconciler(c, scheme)

			doReconcile(t, r, tt.tx.Name)
			if tt.name == "Preparing idempotent: running reconcile twice does not error" {
				// Reset to Preparing to verify a second run is also error-free.
				stored := getTx(t, c, tt.tx.Name)
				stored.Status.Phase = ""
				if err := c.Status().Update(testCtx, stored); err != nil {
					t.Fatalf("reset phase: %v", err)
				}
				doReconcile(t, r, tt.tx.Name)
			}

			tx := getTx(t, c, tt.tx.Name)
			if tx.Status.Phase != tt.wantPhase {
				t.Errorf("phase: want %q, got %q", tt.wantPhase, tx.Status.Phase)
			}
			if tt.wantProgressingStatus != "" {
				assertCondition(t, tx.Status.Conditions, v1alpha1.ChangeConditionProgressing, tt.wantProgressingStatus, tt.wantProgressingReason)
			}
			for _, uuid := range tt.wantSnapshotNilFor {
				snap, ok := tx.Status.SnapshotBefore[uuid]
				if !ok {
					t.Errorf("SnapshotBefore: missing key %q", uuid)
					continue
				}
				if snap != nil {
					t.Errorf("SnapshotBefore[%q]: want nil, got non-nil", uuid)
				}
			}
			for _, uuid := range tt.wantSnapshotSetFor {
				snap, ok := tx.Status.SnapshotBefore[uuid]
				if !ok {
					t.Errorf("SnapshotBefore: missing key %q", uuid)
					continue
				}
				if snap == nil {
					t.Errorf("SnapshotBefore[%q]: want non-nil snapshot, got nil", uuid)
				}
			}
		})
	}
}

// ============================================================================
// TestCRTReconciler_Allocating
// ============================================================================

func TestCRTReconciler_Allocating(t *testing.T) {
	tests := []struct {
		name                    string
		preObjects              []client.Object
		tx                      *v1alpha1.CommittedResourceChange
		wantPhase               v1alpha1.ChangePhase
		wantCRExists            []string
		wantCRAbsent            []string
		wantUpsertedCommitments int
		wantProgressingStatus   metav1.ConditionStatus
		wantProgressingReason   string
		wantFinished            bool
		wantFinishedReason      string
	}{
		{
			name: "API-mode: creates CR and advances to AwaitingScheduling",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-alloc-1"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-1": minimalCRSpec("az-1")},
				},
			},
			wantPhase:               v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRExists:            []string{"commitment-uuid-1"},
			wantUpsertedCommitments: 1,
			wantProgressingStatus:   metav1.ConditionTrue,
			wantProgressingReason:   "Advancing",
		},
		{
			name: "syncer-mode: applies CR and advances directly to Accepted (no scheduling gate)",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-alloc-2"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceSyncer,
					AllowRejection:   false,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-2": minimalCRSpec("az-1")},
				},
			},
			wantPhase:               v1alpha1.ChangePhaseAccepted,
			wantCRExists:            []string{"commitment-uuid-2"},
			wantUpsertedCommitments: 1,
			wantProgressingStatus:   metav1.ConditionFalse,
			wantProgressingReason:   string(v1alpha1.ChangePhaseAccepted),
			wantFinished:            true,
			wantFinishedReason:      string(v1alpha1.ChangePhaseAccepted),
		},
		{
			name: "nil spec deletes CR and it is absent from UpsertedCommitments",
			preObjects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-3"},
					Spec:       *minimalCRSpec("az-1"),
				},
			},
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-alloc-3"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-3": nil},
				},
			},
			wantPhase:               v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRAbsent:            []string{"commitment-uuid-3"},
			wantUpsertedCommitments: 0,
			wantProgressingStatus:   metav1.ConditionTrue,
			wantProgressingReason:   "Advancing",
		},
		{
			name: "pre-existing CR with different spec: spec is overwritten by CreateOrUpdate",
			preObjects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-4"},
					Spec:       *minimalCRSpec("az-old"),
				},
			},
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-alloc-4"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-4": minimalCRSpec("az-new")},
				},
			},
			wantPhase:               v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRExists:            []string{"commitment-uuid-4"},
			wantUpsertedCommitments: 1,
			wantProgressingStatus:   metav1.ConditionTrue,
			wantProgressingReason:   "Advancing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			objs := append([]client.Object{tt.tx}, tt.preObjects...)
			c := testClient(t, scheme, objs...)
			r := testReconciler(c, scheme)

			driveToPhase(t, r, c, tt.tx.Name, tt.wantPhase)

			tx := getTx(t, c, tt.tx.Name)
			if tx.Status.Phase != tt.wantPhase {
				t.Errorf("phase: want %q, got %q", tt.wantPhase, tx.Status.Phase)
			}
			if got := len(tx.Status.UpsertedCommitments); got != tt.wantUpsertedCommitments {
				t.Errorf("UpsertedCommitments len: want %d, got %d", tt.wantUpsertedCommitments, got)
			}
			if tt.wantProgressingStatus != "" {
				assertCondition(t, tx.Status.Conditions, v1alpha1.ChangeConditionProgressing, tt.wantProgressingStatus, tt.wantProgressingReason)
			}
			if tt.wantFinished {
				assertCondition(t, tx.Status.Conditions, v1alpha1.ChangeConditionFinished, metav1.ConditionTrue, tt.wantFinishedReason)
			}
			for _, name := range tt.wantCRExists {
				getCR(t, c, name) // fatals if absent
			}
			for _, name := range tt.wantCRAbsent {
				var cr v1alpha1.CommittedResource
				if err := c.Get(testCtx, types.NamespacedName{Name: name}, &cr); err == nil {
					t.Errorf("CR %q should be absent but exists", name)
				}
			}
		})
	}
}

// TestCRTReconciler_Allocating_Idempotent verifies that re-running Allocating with the same
// desired spec is idempotent: no error, phase advances correctly again.
func TestCRTReconciler_Allocating_Idempotent(t *testing.T) {
	scheme := testScheme(t)
	tx := &v1alpha1.CommittedResourceChange{
		ObjectMeta: metav1.ObjectMeta{Name: "tx-idem"},
		Spec: v1alpha1.CommittedResourceChangeSpec{
			AvailabilityZone: "az-1",
			Source:           v1alpha1.TxSourceAPI,
			AllowRejection:   true,
			NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-idem": minimalCRSpec("az-1")},
		},
	}
	c := testClient(t, scheme, tx)
	r := testReconciler(c, scheme)

	driveToPhase(t, r, c, tx.Name, v1alpha1.ChangePhaseAwaitingScheduling)

	// Reset to Allocating to re-run it.
	stored := getTx(t, c, tx.Name)
	stored.Status.Phase = v1alpha1.ChangePhaseAllocating
	if err := c.Status().Update(testCtx, stored); err != nil {
		t.Fatalf("reset phase to Allocating: %v", err)
	}

	doReconcile(t, r, tx.Name) // second run of Allocating

	final := getTx(t, c, tx.Name)
	if final.Status.Phase != v1alpha1.ChangePhaseAwaitingScheduling {
		t.Errorf("idempotent Allocating: want AwaitingScheduling, got %q", final.Status.Phase)
	}
}

// ============================================================================
// TestCRTReconciler_AwaitingScheduling
// ============================================================================

func TestCRTReconciler_AwaitingScheduling(t *testing.T) {
	const txName = "tx-await"
	const crName = "commitment-uuid-await"

	// setup drives a CRT to AwaitingScheduling and returns the client and reconciler.
	setup := func(t *testing.T, txOpts ...func(*v1alpha1.CommittedResourceChange)) (client.Client, *CommittedResourceChangeReconciler) {
		t.Helper()
		scheme := testScheme(t)
		tx := &v1alpha1.CommittedResourceChange{
			ObjectMeta: metav1.ObjectMeta{Name: txName},
			Spec: v1alpha1.CommittedResourceChangeSpec{
				AvailabilityZone: "az-1",
				Source:           v1alpha1.TxSourceAPI,
				AllowRejection:   true,
				NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-await": minimalCRSpec("az-1")},
			},
		}
		for _, opt := range txOpts {
			opt(tx)
		}
		c := testClient(t, scheme, tx)
		r := testReconciler(c, scheme)
		driveToPhase(t, r, c, txName, v1alpha1.ChangePhaseAwaitingScheduling)
		return c, r
	}

	tests := []struct {
		name                  string
		txOpts                []func(*v1alpha1.CommittedResourceChange)
		setupCR               func(t *testing.T, c client.Client)
		setupTx               func(t *testing.T, c client.Client)
		reconcilerOpt         func(r *CommittedResourceChangeReconciler)
		wantPhase             v1alpha1.ChangePhase
		wantMessage           string // substring that must appear in tx.Status.Message; empty = no check
		wantProgressingStatus metav1.ConditionStatus
		wantProgressingReason string
		wantFinished          bool
		wantFinishedReason    string
	}{
		{
			name: "all CRs accepted → Accepted",
			setupCR: func(t *testing.T, c client.Client) {
				setCRReady(t, c, crName, metav1.ConditionTrue, v1alpha1.CommittedResourceReasonAccepted, "")
			},
			wantPhase:             v1alpha1.ChangePhaseAccepted,
			wantProgressingStatus: metav1.ConditionFalse,
			wantProgressingReason: string(v1alpha1.ChangePhaseAccepted),
			wantFinished:          true,
			wantFinishedReason:    string(v1alpha1.ChangePhaseAccepted),
		},
		{
			name: "DryRun=true and all CRs accepted → DryRunCleanup",
			txOpts: []func(*v1alpha1.CommittedResourceChange){
				func(tx *v1alpha1.CommittedResourceChange) { tx.Spec.DryRun = true },
			},
			setupCR: func(t *testing.T, c client.Client) {
				setCRReady(t, c, crName, metav1.ConditionTrue, v1alpha1.CommittedResourceReasonAccepted, "")
			},
			wantPhase:             v1alpha1.ChangePhaseDryRunCleanup,
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "CR rejected → RollingBack with rejection message",
			setupCR: func(t *testing.T, c client.Client) {
				setCRReady(t, c, crName, metav1.ConditionFalse, v1alpha1.CommittedResourceReasonRejected, "no capacity on host")
			},
			wantPhase:             v1alpha1.ChangePhaseRollingBack,
			wantMessage:           "no capacity on host",
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "stale observedGeneration → stays in AwaitingScheduling",
			setupCR: func(t *testing.T, c client.Client) {
				setCRReadyStaleGen(t, c, crName)
			},
			wantPhase:             v1alpha1.ChangePhaseAwaitingScheduling,
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "ConditionTrue with non-Accepted reason → stays in AwaitingScheduling",
			setupCR: func(t *testing.T, c client.Client) {
				setCRReady(t, c, crName, metav1.ConditionTrue, v1alpha1.CommittedResourceReasonReserving, "still placing")
			},
			wantPhase:             v1alpha1.ChangePhaseAwaitingScheduling,
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
		{
			name: "timeout exceeded → RollingBack with timed out message",
			reconcilerOpt: func(r *CommittedResourceChangeReconciler) {
				r.AwaitingSchedulingTimeout = 50 * time.Millisecond
			},
			setupTx: func(t *testing.T, c client.Client) {
				backdateProgressingCondition(t, c, txName, 1*time.Second)
			},
			wantPhase:             v1alpha1.ChangePhaseRollingBack,
			wantMessage:           "timed out",
			wantProgressingStatus: metav1.ConditionFalse,
			wantProgressingReason: v1alpha1.ChangeReasonTimedOut,
		},
		{
			name: "timeout=0 means no timeout: CRT stays in AwaitingScheduling indefinitely",
			// No reconcilerOpt, so AwaitingSchedulingTimeout=0 (disabled).
			// CR ready condition is never set → should stay in AwaitingScheduling.
			wantPhase:             v1alpha1.ChangePhaseAwaitingScheduling,
			wantProgressingStatus: metav1.ConditionTrue,
			wantProgressingReason: "Advancing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r := setup(t, tt.txOpts...)

			if tt.reconcilerOpt != nil {
				tt.reconcilerOpt(r)
			}
			if tt.setupCR != nil {
				tt.setupCR(t, c)
			}
			if tt.setupTx != nil {
				tt.setupTx(t, c)
			}

			doReconcile(t, r, txName)

			tx := getTx(t, c, txName)
			if tx.Status.Phase != tt.wantPhase {
				t.Errorf("phase: want %q, got %q", tt.wantPhase, tx.Status.Phase)
			}
			if tt.wantMessage != "" && !strings.Contains(tx.Status.Message, tt.wantMessage) {
				t.Errorf("message: want substring %q in %q", tt.wantMessage, tx.Status.Message)
			}
			if tt.wantProgressingStatus != "" {
				assertCondition(t, tx.Status.Conditions, v1alpha1.ChangeConditionProgressing, tt.wantProgressingStatus, tt.wantProgressingReason)
			}
			if tt.wantFinished {
				assertCondition(t, tx.Status.Conditions, v1alpha1.ChangeConditionFinished, metav1.ConditionTrue, tt.wantFinishedReason)
			}
		})
	}
}

// setPhase directly sets a CRT's phase via Status().Update for test setup.
func setPhase(t *testing.T, c client.Client, txName string, phase v1alpha1.ChangePhase) {
	t.Helper()
	tx := getTx(t, c, txName)
	tx.Status.Phase = phase
	if err := c.Status().Update(testCtx, tx); err != nil {
		t.Fatalf("setPhase %q on %q: %v", phase, txName, err)
	}
}

// reconcileN calls doReconcile exactly n times without inspecting the phase between calls.
func reconcileN(t *testing.T, r *CommittedResourceChangeReconciler, name string, n int) {
	t.Helper()
	for range n {
		doReconcile(t, r, name)
	}
}

// ============================================================================
// TestCRTReconciler_Idempotency
// ============================================================================

// TestCRTReconciler_Idempotency verifies that phase handlers are re-entrant: running the same
// handler multiple times (simulating crash-then-requeue) produces the same end state.
// The fake client never returns transient errors; these tests confirm re-entrancy — the property
// that makes controller-runtime's error-then-requeue pattern safe.
func TestCRTReconciler_Idempotency(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Preparing: snapshot is consistent across re-runs",
			run: func(t *testing.T) {
				scheme := testScheme(t)
				tx := &v1alpha1.CommittedResourceChange{
					ObjectMeta: metav1.ObjectMeta{Name: "tx-idem-1"},
					Spec: v1alpha1.CommittedResourceChangeSpec{
						AvailabilityZone: "az-1", Source: v1alpha1.TxSourceAPI, AllowRejection: true,
						NewState: map[string]*v1alpha1.CommittedResourceSpec{"uuid-idem-1": minimalCRSpec("az-1")},
					},
				}
				c := testClient(t, scheme, tx)
				r := testReconciler(c, scheme)

				doReconcile(t, r, tx.Name) // Preparing → Allocating
				snap1 := getTx(t, c, tx.Name).Status.SnapshotBefore

				setPhase(t, c, tx.Name, "") // reset to Preparing
				doReconcile(t, r, tx.Name)  // Preparing → Allocating (re-run)
				snap2 := getTx(t, c, tx.Name).Status.SnapshotBefore

				for i, snap := range []map[string]*v1alpha1.CommittedResourceSpec{snap1, snap2} {
					v, ok := snap["uuid-idem-1"]
					if !ok {
						t.Errorf("run %d: snapshot missing uuid-idem-1", i+1)
					} else if v != nil {
						t.Errorf("run %d: snapshot[uuid-idem-1]: want nil, got non-nil", i+1)
					}
				}
			},
		},
		{
			name: "Allocating: UpsertedCommitments and CR spec stable across re-runs",
			run: func(t *testing.T) {
				scheme := testScheme(t)
				tx := &v1alpha1.CommittedResourceChange{
					ObjectMeta: metav1.ObjectMeta{Name: "tx-idem-2"},
					Spec: v1alpha1.CommittedResourceChangeSpec{
						AvailabilityZone: "az-1", Source: v1alpha1.TxSourceAPI, AllowRejection: true,
						NewState: map[string]*v1alpha1.CommittedResourceSpec{"uuid-idem-2": minimalCRSpec("az-1")},
					},
				}
				c := testClient(t, scheme, tx)
				r := testReconciler(c, scheme)

				driveToPhase(t, r, c, tx.Name, v1alpha1.ChangePhaseAwaitingScheduling)
				upserted1 := getTx(t, c, tx.Name).Status.UpsertedCommitments

				setPhase(t, c, tx.Name, v1alpha1.ChangePhaseAllocating) // reset to Allocating
				doReconcile(t, r, tx.Name)                              // Allocating → AwaitingScheduling (re-run)
				upserted2 := getTx(t, c, tx.Name).Status.UpsertedCommitments

				if len(upserted1) != 1 || len(upserted2) != 1 {
					t.Errorf("UpsertedCommitments: want len 1 on each run, got %d, %d", len(upserted1), len(upserted2))
				}
				if len(upserted1) > 0 && len(upserted2) > 0 && upserted1[0].Name != upserted2[0].Name {
					t.Errorf("UpsertedCommitments name changed: %q → %q", upserted1[0].Name, upserted2[0].Name)
				}
				getCR(t, c, "commitment-uuid-idem-2") // CR must still exist
			},
		},
		{
			name: "AwaitingScheduling: multiple calls without CR ready stay waiting",
			run: func(t *testing.T) {
				scheme := testScheme(t)
				tx := &v1alpha1.CommittedResourceChange{
					ObjectMeta: metav1.ObjectMeta{Name: "tx-idem-3"},
					Spec: v1alpha1.CommittedResourceChangeSpec{
						AvailabilityZone: "az-1", Source: v1alpha1.TxSourceAPI, AllowRejection: true,
						NewState: map[string]*v1alpha1.CommittedResourceSpec{"uuid-idem-3": minimalCRSpec("az-1")},
					},
				}
				c := testClient(t, scheme, tx)
				r := testReconciler(c, scheme)

				driveToPhase(t, r, c, tx.Name, v1alpha1.ChangePhaseAwaitingScheduling)
				reconcileN(t, r, tx.Name, 3) // CR never set ready — must stay in AwaitingScheduling

				if got := getTx(t, c, tx.Name).Status.Phase; got != v1alpha1.ChangePhaseAwaitingScheduling {
					t.Errorf("phase: want AwaitingScheduling, got %q", got)
				}
			},
		},
		{
			name: "AwaitingScheduling → Accepted stays Accepted on repeated reconciles",
			run: func(t *testing.T) {
				scheme := testScheme(t)
				tx := &v1alpha1.CommittedResourceChange{
					ObjectMeta: metav1.ObjectMeta{Name: "tx-idem-4"},
					Spec: v1alpha1.CommittedResourceChangeSpec{
						AvailabilityZone: "az-1", Source: v1alpha1.TxSourceAPI, AllowRejection: true,
						NewState: map[string]*v1alpha1.CommittedResourceSpec{"uuid-idem-4": minimalCRSpec("az-1")},
					},
				}
				c := testClient(t, scheme, tx)
				r := testReconciler(c, scheme)

				driveToPhase(t, r, c, tx.Name, v1alpha1.ChangePhaseAwaitingScheduling)
				setCRReady(t, c, "commitment-uuid-idem-4", metav1.ConditionTrue, v1alpha1.CommittedResourceReasonAccepted, "")
				reconcileN(t, r, tx.Name, 3) // first call → Accepted; subsequent calls → terminal no-op

				if got := getTx(t, c, tx.Name).Status.Phase; got != v1alpha1.ChangePhaseAccepted {
					t.Errorf("phase: want Accepted, got %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// ============================================================================
// TestCRTReconciler_APIScenarios
// ============================================================================

// crExists returns true if a CommittedResource with the given name exists.
func crExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	var cr v1alpha1.CommittedResource
	err := c.Get(testCtx, types.NamespacedName{Name: name}, &cr)
	if err == nil {
		return true
	}
	if client.IgnoreNotFound(err) == nil {
		return false
	}
	t.Fatalf("crExists(%q): %v", name, err)
	return false
}

// TestCRTReconciler_APIScenarios covers realistic multi-CR change patterns as seen from the
// CR acceptance API: mixed creates, updates, and deletes in a single transaction.
func TestCRTReconciler_APIScenarios(t *testing.T) {
	tests := []struct {
		name string
		// pre-existing CRs before the transaction is created
		preObjects []client.Object
		// the CRT to create
		tx *v1alpha1.CommittedResourceChange
		// expected phase after driving the FSM to a stable point
		wantPhase v1alpha1.ChangePhase
		// CR names that must exist after Allocating
		wantCRExists []string
		// CR names that must be absent after Allocating
		wantCRAbsent []string
		// exact UpsertedCommitments count (nils are not upserted)
		wantUpsertedCount int
		// UUIDs that must have nil snapshot (CR did not exist before)
		wantSnapshotNilFor []string
		// UUIDs that must have non-nil snapshot (CR existed before)
		wantSnapshotSetFor []string
	}{
		{
			name: "multiple new CRs: all created, all upserted",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-api-1"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState: map[string]*v1alpha1.CommittedResourceSpec{
						"uuid-a1": minimalCRSpec("az-1"),
						"uuid-a2": minimalCRSpec("az-1"),
						"uuid-a3": minimalCRSpec("az-1"),
					},
				},
			},
			wantPhase:          v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRExists:       []string{"commitment-uuid-a1", "commitment-uuid-a2", "commitment-uuid-a3"},
			wantUpsertedCount:  3,
			wantSnapshotNilFor: []string{"uuid-a1", "uuid-a2", "uuid-a3"},
		},
		{
			name: "one new CR + one existing CR updated: both upserted, snapshot captures existing",
			preObjects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-b2"},
					Spec:       *minimalCRSpec("az-old"),
				},
			},
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-api-2"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState: map[string]*v1alpha1.CommittedResourceSpec{
						"uuid-b1": minimalCRSpec("az-1"),   // new
						"uuid-b2": minimalCRSpec("az-new"), // existing, spec updated
					},
				},
			},
			wantPhase:          v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRExists:       []string{"commitment-uuid-b1", "commitment-uuid-b2"},
			wantUpsertedCount:  2,
			wantSnapshotNilFor: []string{"uuid-b1"},
			wantSnapshotSetFor: []string{"uuid-b2"},
		},
		{
			name: "one CR deleted + one CR updated: deleted absent, only updated upserted",
			preObjects: []client.Object{
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-c1"},
					Spec:       *minimalCRSpec("az-1"),
				},
				&v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "commitment-uuid-c2"},
					Spec:       *minimalCRSpec("az-1"),
				},
			},
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-api-3"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState: map[string]*v1alpha1.CommittedResourceSpec{
						"uuid-c1": nil,                   // delete
						"uuid-c2": minimalCRSpec("az-1"), // update
					},
				},
			},
			wantPhase:          v1alpha1.ChangePhaseAwaitingScheduling,
			wantCRExists:       []string{"commitment-uuid-c2"},
			wantCRAbsent:       []string{"commitment-uuid-c1"},
			wantUpsertedCount:  1,
			wantSnapshotSetFor: []string{"uuid-c1", "uuid-c2"},
		},
		{
			name: "empty NewState: no CRs touched, advances all the way to Accepted",
			tx: &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-api-4"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
					AllowRejection:   true,
					NewState:         map[string]*v1alpha1.CommittedResourceSpec{},
				},
			},
			// AwaitingScheduling with empty UpsertedCommitments → allAccepted=true immediately → Accepted
			wantPhase:         v1alpha1.ChangePhaseAccepted,
			wantUpsertedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			objs := append([]client.Object{tt.tx}, tt.preObjects...)
			c := testClient(t, scheme, objs...)
			r := testReconciler(c, scheme)

			driveToPhase(t, r, c, tt.tx.Name, tt.wantPhase)

			tx := getTx(t, c, tt.tx.Name)
			if tx.Status.Phase != tt.wantPhase {
				t.Errorf("phase: want %q, got %q", tt.wantPhase, tx.Status.Phase)
			}
			if got := len(tx.Status.UpsertedCommitments); got != tt.wantUpsertedCount {
				t.Errorf("UpsertedCommitments count: want %d, got %d", tt.wantUpsertedCount, got)
			}
			for _, name := range tt.wantCRExists {
				if !crExists(t, c, name) {
					t.Errorf("CR %q should exist but is absent", name)
				}
			}
			for _, name := range tt.wantCRAbsent {
				if crExists(t, c, name) {
					t.Errorf("CR %q should be absent but exists", name)
				}
			}
			for _, uuid := range tt.wantSnapshotNilFor {
				snap, ok := tx.Status.SnapshotBefore[uuid]
				if !ok {
					t.Errorf("SnapshotBefore: missing key %q", uuid)
				} else if snap != nil {
					t.Errorf("SnapshotBefore[%q]: want nil, got non-nil", uuid)
				}
			}
			for _, uuid := range tt.wantSnapshotSetFor {
				snap, ok := tx.Status.SnapshotBefore[uuid]
				if !ok {
					t.Errorf("SnapshotBefore: missing key %q", uuid)
				} else if snap == nil {
					t.Errorf("SnapshotBefore[%q]: want non-nil, got nil", uuid)
				}
			}
		})
	}
}

// ============================================================================
// TestCRTReconciler_EdgeCases
// ============================================================================

// TestCRTReconciler_EdgeCases covers paths that don't fit the main FSM tables.
func TestCRTReconciler_EdgeCases(t *testing.T) {
	t.Run("DeletionTimestamp: reconcile is a no-op", func(t *testing.T) {
		scheme := testScheme(t)
		now := metav1.Now()
		tx := &v1alpha1.CommittedResourceChange{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "tx-del",
				DeletionTimestamp: &now,
				// Finalizers required for the fake client to allow a non-zero DeletionTimestamp.
				Finalizers: []string{"test.io/hold"},
			},
			Spec: v1alpha1.CommittedResourceChangeSpec{
				AvailabilityZone: "az-1",
				Source:           v1alpha1.TxSourceAPI,
			},
		}
		c := testClient(t, scheme, tx)
		r := testReconciler(c, scheme)

		doReconcile(t, r, tx.Name) // must not error, must not advance phase

		got := getTx(t, c, tx.Name).Status.Phase
		if got != "" {
			t.Errorf("phase: want empty (no-op), got %q", got)
		}
	})

	t.Run("unknown phase returns error", func(t *testing.T) {
		scheme := testScheme(t)
		tx := &v1alpha1.CommittedResourceChange{
			ObjectMeta: metav1.ObjectMeta{Name: "tx-unknown"},
			Spec: v1alpha1.CommittedResourceChangeSpec{
				AvailabilityZone: "az-1",
				Source:           v1alpha1.TxSourceAPI,
			},
		}
		c := testClient(t, scheme, tx)
		r := testReconciler(c, scheme)

		// Seed an unknown phase.
		tx.Status.Phase = "Bogus"
		if err := c.Status().Update(testCtx, tx); err != nil {
			t.Fatalf("seed phase: %v", err)
		}

		_, err := r.Reconcile(testCtx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tx.Name}})
		if err == nil {
			t.Error("expected error for unknown phase, got nil")
		}
	})

	t.Run("crToTransaction maps CR name to owning CRT via field index", func(t *testing.T) {
		scheme := testScheme(t)
		tx := &v1alpha1.CommittedResourceChange{
			ObjectMeta: metav1.ObjectMeta{Name: "tx-map"},
			Spec: v1alpha1.CommittedResourceChangeSpec{
				AvailabilityZone: "az-1",
				Source:           v1alpha1.TxSourceAPI,
				AllowRejection:   true,
				NewState:         map[string]*v1alpha1.CommittedResourceSpec{"uuid-map": minimalCRSpec("az-1")},
			},
		}
		c := testClient(t, scheme, tx)
		r := testReconciler(c, scheme)

		// Drive to AwaitingScheduling so UpsertedCommitments is populated.
		driveToPhase(t, r, c, tx.Name, v1alpha1.ChangePhaseAwaitingScheduling)

		// crToTransaction should return a request for the owning CRT.
		cr := getCR(t, c, "commitment-uuid-map")
		reqs := r.crToChangeRequest(testCtx, cr)
		if len(reqs) != 1 {
			t.Fatalf("crToTransaction: want 1 request, got %d", len(reqs))
		}
		if reqs[0].Name != tx.Name {
			t.Errorf("crToTransaction: want request for %q, got %q", tx.Name, reqs[0].Name)
		}
	})
}

// ============================================================================
// TestCRTReconciler_Terminal
// ============================================================================

func TestCRTReconciler_Terminal(t *testing.T) {
	tests := []struct {
		name  string
		phase v1alpha1.ChangePhase
	}{
		{"Accepted is a no-op", v1alpha1.ChangePhaseAccepted},
		{"Rejected is a no-op", v1alpha1.ChangePhaseRejected},
		{"Superseded is a no-op", v1alpha1.ChangePhaseSuperseded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			tx := &v1alpha1.CommittedResourceChange{
				ObjectMeta: metav1.ObjectMeta{Name: "tx-term"},
				Spec: v1alpha1.CommittedResourceChangeSpec{
					AvailabilityZone: "az-1",
					Source:           v1alpha1.TxSourceAPI,
				},
			}
			c := testClient(t, scheme, tx)
			r := testReconciler(c, scheme)

			// Seed the terminal phase directly via Status().Update.
			tx.Status.Phase = tt.phase
			if err := c.Status().Update(testCtx, tx); err != nil {
				t.Fatalf("seed phase %q: %v", tt.phase, err)
			}

			doReconcile(t, r, tx.Name) // must not error

			// Phase must remain unchanged.
			got := getTx(t, c, tx.Name).Status.Phase
			if got != tt.phase {
				t.Errorf("terminal phase changed: want %q, got %q", tt.phase, got)
			}
		})
	}
}

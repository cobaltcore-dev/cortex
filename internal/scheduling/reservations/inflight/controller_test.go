// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"errors"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// newTestScheme returns a runtime.Scheme with all required types registered.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add hypervisor scheme: %v", err)
	}
	if err := vmv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add vm-operator scheme: %v", err)
	}
	return s
}

// newTestClient builds a fake client with the indices the controller relies on.
func newTestClient(scheme *runtime.Scheme, objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Reservation{}, &vmv1alpha1.VirtualMachine{}).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByOperationID, indexReservationByOperationID).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByVMID, indexReservationByVMID).
		Build()
}

// newTestController builds a Controller wired with the VirtualMachine owner
// handler, mirroring the production registry so Reconcile can dispatch.
func newTestController(k8sClient client.Client) *Controller {
	vmHandler := &VirtualMachineHandler{Client: k8sClient}
	return &Controller{
		Client:   k8sClient,
		handlers: map[string]OwnerHandler{vmHandler.Kind(): vmHandler},
	}
}

// inFlightOpts configures the in-flight reservation built by newInFlightReservation.
type inFlightOpts struct {
	intent      v1alpha1.SchedulingIntent
	operationID string
	resources   map[hv1.ResourceName]resource.Quantity
	endTime     *metav1.Time
	createdAt   *metav1.Time // reservation creationTimestamp, for condition-freshness checks
	labels      map[string]string
	ownerRefs   []metav1.OwnerReference // defaults to a VirtualMachine owner when nil
}

// newInFlightReservation builds an in-flight reservation with the given name, VM
// ID and target host plus optional extras. Unless overridden via opts.ownerRefs,
// the reservation carries a VirtualMachine owner reference so it dispatches to
// the VM owner handler (in-flight reservations require a VM CRD owner).
func newInFlightReservation(name, vmID, targetHost string, opts inFlightOpts) *v1alpha1.Reservation {
	ownerRefs := opts.ownerRefs
	if ownerRefs == nil {
		ownerRefs = []metav1.OwnerReference{{Kind: "VirtualMachine"}}
	}
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: opts.labels, OwnerReferences: ownerRefs},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			TargetHost:       targetHost,
			Resources:        opts.resources,
			EndTime:          opts.endTime,
			InFlightReservation: &v1alpha1.InFlightReservationSpec{
				VMID:        vmID,
				Intent:      opts.intent,
				OperationID: opts.operationID,
			},
		},
	}
	if opts.createdAt != nil {
		res.CreationTimestamp = *opts.createdAt
	}
	return res
}

func exists(t *testing.T, k8sClient client.Client, name string) bool {
	t.Helper()
	var got v1alpha1.Reservation
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &got)
	return err == nil
}

func assertReadyCondition(t *testing.T, k8sClient client.Client, name string, wantStatus metav1.ConditionStatus, wantReason string) {
	t.Helper()
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &got); err != nil {
		t.Fatalf("failed to get reservation %q: %v", name, err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ReservationConditionReady)
	if cond == nil {
		t.Fatalf("Ready condition was not set on %q", name)
	}
	if cond.Status != wantStatus {
		t.Errorf("%s: Ready status = %q, want %q", name, cond.Status, wantStatus)
	}
	if cond.Reason != wantReason {
		t.Errorf("%s: Ready reason = %q, want %q", name, cond.Reason, wantReason)
	}
}

func TestReconcile(t *testing.T) {
	cpu4 := map[hv1.ResourceName]resource.Quantity{"cpu": resource.MustParse("4")}
	past := metav1.NewTime(time.Now().Add(-time.Minute))
	future := metav1.NewTime(time.Now().Add(time.Hour))
	// Freshness is judged against the reservation's creationTimestamp. Set it
	// explicitly (the fake client does not stamp it) so a condition's
	// LastTransitionTime can be placed clearly before (stale) or after (fresh).
	resCreated := metav1.NewTime(time.Now())
	freshFuture := metav1.NewTime(resCreated.Add(time.Hour))
	stalePast := metav1.NewTime(resCreated.Add(-time.Hour))
	createA := inFlightOpts{operationID: "op-a", intent: novaapi.CreateIntent}
	expiredA := inFlightOpts{operationID: "op-a", endTime: &past, intent: novaapi.CreateIntent}

	type testCase struct {
		name            string
		objects         []client.Object
		reconcile       string
		wantErr         error
		wantRequeue     time.Duration
		wantDeleted     []string
		wantKept        []string
		wantReadyStatus metav1.ConditionStatus
		wantReadyReason string
		wantAnomaly     string
	}
	tests := []testCase{
		{name: "not found is ignored", reconcile: "missing"},
		{
			name: "missing spec sets condition false",
			objects: []client.Object{&v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "res-1"},
				Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeInFlight},
			}},
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionFalse, wantReadyReason: "MissingSpec",
		},
		{
			name: "no owner handler sets condition and metric",
			objects: []client.Object{&v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "res-1", OwnerReferences: []metav1.OwnerReference{{Kind: "ReschedulingRequest"}}},
				Spec: v1alpha1.ReservationSpec{
					Type:                v1alpha1.ReservationTypeInFlight,
					SchedulingDomain:    v1alpha1.SchedulingDomainNova,
					InFlightReservation: &v1alpha1.InFlightReservationSpec{}, // no VMID -> no fallback
				},
			}},
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "NoOwnerHandler",
			wantAnomaly: AnomalyNoOwnerHandler,
		},
		{
			// No VM CRD yet: there is no terminal signal, so the hold is kept and
			// the controller waits (TTL is the only release for an abandoned op).
			name:            "no VM CRD requeues",
			objects:         []client.Object{newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.CreateIntent})},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "AwaitingPlacement",
		},
		{
			// UC1: VM landed and running on a different host than this candidate
			// targeted; the placement is confirmed so the stale candidate is
			// released.
			name: "create: VM placed and running on different host releases stale candidate",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.CreateIntent}),
				newVM("vm-uuid-1", "host-2", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running")),
			},
			wantDeleted: []string{"res-1"},
		},
		{
			// UC1: reconciling the losing candidate releases the whole group once
			// the VM is confirmed placed and running.
			name: "multi-candidate group released on landing",
			objects: []client.Object{
				newInFlightReservation("a", "vm-uuid-1", "host-1", createA),
				newInFlightReservation("b", "vm-uuid-1", "host-2", createA),
				newInFlightReservation("c", "vm-uuid-1", "host-3", createA),
				newVM("vm-uuid-1", "host-2", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running")),
			},
			reconcile:   "a",
			wantDeleted: []string{"a", "b", "c"},
		},
		{
			// Completing op-a must leave a concurrent op-b on the same VM untouched.
			name: "operation ID scoped delete",
			objects: []client.Object{
				newInFlightReservation("a1", "vm-uuid-1", "host-1", inFlightOpts{operationID: "op-a", intent: novaapi.CreateIntent}),
				newInFlightReservation("a2", "vm-uuid-1", "host-2", inFlightOpts{operationID: "op-a", intent: novaapi.CreateIntent}),
				newInFlightReservation("b1", "vm-uuid-1", "host-1", inFlightOpts{operationID: "op-b", intent: novaapi.CreateIntent}),
				newVM("vm-uuid-1", "host-1", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running")),
			},
			reconcile:   "a1",
			wantDeleted: []string{"a1", "a2"},
			wantKept:    []string{"b1"},
		},
		{
			// No VM CRD hosts the VM -> without TTL this would just requeue.
			name: "TTL expiry releases operation group",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", expiredA),
				newInFlightReservation("res-2", "vm-uuid-1", "host-2", expiredA),
			},
			wantDeleted: []string{"res-1", "res-2"},
		},
		{
			name:        "TTL not yet exceeded requeues",
			objects:     []client.Object{newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{endTime: &future})},
			wantRequeue: requeueInterval,
			wantKept:    []string{"res-1"},
		},
		{
			// Live migration with a VM CRD that reports an ongoing migration: the
			// holds are kept until the migration settles or the TTL backstop fires.
			name: "non-create: live migration ongoing is kept",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.LiveMigrationIntent, resources: cpu4}),
				newVM("vm-uuid-1", "host-1", cond(vmv1alpha1.ConditionTypeMigrating, metav1.ConditionTrue, vmv1alpha1.ConditionReasonMigratingOngoing)),
			},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "Migrating",
		},
		// --- VM-CRD driven cases (primary release path through Reconcile) ---
		{
			// UC1: VM CRD reports placed and Running; the whole create group is
			// released through Reconcile.
			name: "create: VM placed and running releases group",
			objects: []client.Object{
				newInFlightReservation("a", "vm-uuid-1", "host-1", createA),
				newInFlightReservation("b", "vm-uuid-1", "host-2", createA),
				newVM("vm-uuid-1", "host-1", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running")),
			},
			reconcile:   "a",
			wantDeleted: []string{"a", "b"},
		},
		{
			// UC1: Nova ACTIVE on a placed VM is also a live signal.
			name: "create: VM placed and nova ACTIVE deletes reservation",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.CreateIntent}),
				withNovaStatus(newVM("vm-uuid-1", "host-1"), "ACTIVE"),
			},
			wantDeleted: []string{"res-1"},
		},
		{
			// UC1: placed but not yet live (Nova BUILD) must keep blocking.
			name: "create: VM building requeues",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.CreateIntent}),
				withNovaStatus(newVM("vm-uuid-1", "host-1"), "BUILD"),
			},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "AwaitingPlacement",
		},
		{
			// UC1: Nova ERROR releases a failed initial placement through Reconcile.
			name: "create: nova ERROR deletes reservation",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.CreateIntent}),
				withNovaStatus(newVM("vm-uuid-1", ""), "ERROR"),
			},
			wantDeleted: []string{"res-1"},
		},
		{
			// UC2: an ongoing resize keeps the hold and surfaces the Resizing reason.
			name: "resize: ongoing requeues",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.ResizeIntent}),
				newVM("vm-uuid-1", "host-1", cond(vmv1alpha1.ConditionTypeResizing, metav1.ConditionTrue, vmv1alpha1.ConditionReasonResizingOngoing)),
			},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "Resizing",
		},
		{
			// UC2: a terminal Resizing reason that transitioned before the hold was
			// created is stale (previous operation) and must not release it.
			name: "resize: stale terminal condition is kept",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.ResizeIntent, createdAt: &resCreated}),
				vmWithCond("vm-uuid-1", vmv1alpha1.ConditionTypeResizing, vmv1alpha1.ConditionReasonResizingCompleted, stalePast),
			},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "Resizing",
		},
		{
			// UC3: an ongoing live migration keeps both holds.
			name: "live migration: ongoing requeues",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.LiveMigrationIntent}),
				newVM("vm-uuid-1", "host-1", cond(vmv1alpha1.ConditionTypeMigrating, metav1.ConditionTrue, vmv1alpha1.ConditionReasonMigratingOngoing)),
			},
			wantRequeue:     requeueInterval,
			wantKept:        []string{"res-1"},
			wantReadyStatus: metav1.ConditionUnknown, wantReadyReason: "Migrating",
		},
		{
			// UC3: a fresh terminal migration releases both target and source holds
			// (the whole operation group) through Reconcile.
			name: "live migration: fresh terminal releases target and source group",
			objects: []client.Object{
				newInFlightReservation("target", "vm-uuid-1", "host-2", inFlightOpts{operationID: "op-m", intent: novaapi.LiveMigrationIntent, createdAt: &resCreated}),
				newInFlightReservation("source", "vm-uuid-1", "host-1", inFlightOpts{operationID: "op-m", intent: novaapi.LiveMigrationIntent, createdAt: &resCreated}),
				vmWithCond("vm-uuid-1", vmv1alpha1.ConditionTypeMigrating, vmv1alpha1.ConditionReasonMigratingCompleted, freshFuture),
			},
			reconcile:   "source",
			wantDeleted: []string{"target", "source"},
		},
	}
	// Every terminal resize reason, when fresh, releases the hold through Reconcile.
	for _, r := range []string{
		vmv1alpha1.ConditionReasonResizingCompleted,
		vmv1alpha1.ConditionReasonResizingReverted,
		vmv1alpha1.ConditionReasonResizingFailed,
	} {
		tests = append(tests, testCase{
			name: "resize: fresh terminal " + r + " deletes reservation",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.ResizeIntent, createdAt: &resCreated}),
				vmWithCond("vm-uuid-1", vmv1alpha1.ConditionTypeResizing, r, freshFuture),
			},
			wantDeleted: []string{"res-1"},
		})
	}
	// Every terminal live-migration reason, when fresh, releases the hold.
	for _, r := range []string{
		vmv1alpha1.ConditionReasonMigratingCompleted,
		vmv1alpha1.ConditionReasonMigratingRolledBack,
		vmv1alpha1.ConditionReasonMigratingAborting,
		vmv1alpha1.ConditionReasonMigratingFailed,
	} {
		tests = append(tests, testCase{
			name: "live migration: fresh terminal " + r + " deletes reservation",
			objects: []client.Object{
				newInFlightReservation("res-1", "vm-uuid-1", "host-1", inFlightOpts{intent: novaapi.LiveMigrationIntent, createdAt: &resCreated}),
				vmWithCond("vm-uuid-1", vmv1alpha1.ConditionTypeMigrating, r, freshFuture),
			},
			wantDeleted: []string{"res-1"},
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := newTestClient(newTestScheme(t), tt.objects...)
			c := newTestController(k8sClient)
			c.Monitor = NewMonitor(nil)
			name := tt.reconcile
			if name == "" {
				name = "res-1"
			}
			got, err := c.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Reconcile err = %v, want %v", err, tt.wantErr)
			}
			if got.RequeueAfter != tt.wantRequeue {
				t.Errorf("RequeueAfter = %v, want %v", got.RequeueAfter, tt.wantRequeue)
			}
			for _, n := range tt.wantDeleted {
				if exists(t, k8sClient, n) {
					t.Errorf("expected %s to be deleted", n)
				}
			}
			for _, n := range tt.wantKept {
				if !exists(t, k8sClient, n) {
					t.Errorf("expected %s to be kept", n)
				}
			}
			if tt.wantReadyReason != "" {
				assertReadyCondition(t, k8sClient, name, tt.wantReadyStatus, tt.wantReadyReason)
			}
			if tt.wantAnomaly != "" {
				if v := testutil.ToFloat64(c.Monitor.anomaliesVec.WithLabelValues(tt.wantAnomaly)); v != 1 {
					t.Errorf("%s anomaly count = %v, want 1", tt.wantAnomaly, v)
				}
			}
		})
	}
}

func TestReconcile_UnexpectedTypeIsIgnored(t *testing.T) {
	// A non-in-flight reservation must not be touched: its status belongs to
	// another controller. Reconcile logs and returns without writing status.
	scheme := newTestScheme(t)
	wrong := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "wrong-type"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover},
	}
	k8sClient := newTestClient(scheme, wrong)
	c := newTestController(k8sClient)
	if _, err := c.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "wrong-type"}}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "wrong-type"}, &got); err != nil {
		t.Fatalf("failed to get reservation: %v", err)
	}
	if len(got.Status.Conditions) != 0 {
		t.Errorf("expected no status conditions on foreign reservation, got %+v", got.Status.Conditions)
	}
}

func TestHandlerFor(t *testing.T) {
	vmOwned := metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "VirtualMachine"}}}
	reschedOwned := metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "Rescheduling"}}}
	novaVM := v1alpha1.ReservationSpec{
		SchedulingDomain:    v1alpha1.SchedulingDomainNova,
		InFlightReservation: &v1alpha1.InFlightReservationSpec{VMID: "vm-1"},
	}
	emptySpec := v1alpha1.ReservationSpec{InFlightReservation: &v1alpha1.InFlightReservationSpec{}}

	tests := []struct {
		name        string
		extra       []string // additionally registered stub handler kinds
		res         *v1alpha1.Reservation
		wantKind    string // "" means no handler
		wantAnomaly string
	}{
		{name: "owner reference kind", res: &v1alpha1.Reservation{ObjectMeta: vmOwned, Spec: emptySpec}, wantKind: "VirtualMachine"},
		// A VM subject without any owner reference is no longer handled: in-flight
		// reservations must carry a VirtualMachine owner reference.
		{name: "vmID without owner ref is not handled", res: &v1alpha1.Reservation{Spec: novaVM}},
		{name: "unknown owner, no vmID", res: &v1alpha1.Reservation{ObjectMeta: reschedOwned, Spec: emptySpec}},
		// An unregistered owner carrying a vmID must not fall back to the VM handler.
		{name: "unregistered owner with vmID does not fall back to VM", res: &v1alpha1.Reservation{ObjectMeta: reschedOwned, Spec: novaVM}},
		{name: "registered non-VM owner wins over vmID", extra: []string{"Rescheduling"}, res: &v1alpha1.Reservation{ObjectMeta: reschedOwned, Spec: novaVM}, wantKind: "Rescheduling"},
		{
			// An owner reference is present and other handlers are registered, but
			// none matches this owner's kind: no handler, no ambiguity.
			name:  "owner present but no handler matches",
			extra: []string{"Rescheduling", "Backup"},
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "Snapshot"}}},
				Spec:       emptySpec,
			},
		},
		{
			// With more than two handlers registered, the one matching the owner's
			// kind is selected (not the first registered).
			name:     "selects correct handler among several",
			extra:    []string{"Rescheduling", "Backup", "Snapshot"},
			res:      &v1alpha1.Reservation{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "Backup"}}}, Spec: emptySpec},
			wantKind: "Backup",
		},
		{
			// Several handlers registered and several owner references, only one of
			// which maps to a registered handler: that one wins, unambiguously.
			name:  "one of several owner refs matches among several handlers",
			extra: []string{"Rescheduling", "Backup", "Snapshot"},
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "Unknown"}, {Kind: "Snapshot"}}},
				Spec:       emptySpec,
			},
			wantKind: "Snapshot",
		},
		{
			name:  "ambiguous owner records metric",
			extra: []string{"ReschedulingRequest"},
			res: &v1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "VirtualMachine"}, {Kind: "ReschedulingRequest"}}},
				Spec:       emptySpec,
			},
			wantKind:    "*", // any handler, chosen deterministically
			wantAnomaly: AnomalyAmbiguousOwner,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestController(newTestClient(newTestScheme(t)))
			c.Monitor = NewMonitor(nil)
			for _, k := range tt.extra {
				c.handlers[k] = &stubHandler{kind: k}
			}
			h := c.handlerFor(context.Background(), tt.res)
			switch {
			case tt.wantKind == "" && h != nil:
				t.Errorf("expected no handler, got %s", h.Kind())
			case tt.wantKind != "" && h == nil:
				t.Fatalf("expected %s handler, got nil", tt.wantKind)
			case tt.wantKind != "" && tt.wantKind != "*" && h.Kind() != tt.wantKind:
				t.Errorf("handler kind = %s, want %s", h.Kind(), tt.wantKind)
			}
			if tt.wantAnomaly != "" {
				if v := testutil.ToFloat64(c.Monitor.anomaliesVec.WithLabelValues(tt.wantAnomaly)); v != 1 {
					t.Errorf("%s anomaly count = %v, want 1", tt.wantAnomaly, v)
				}
			}
		})
	}
}

// stubHandler is a minimal OwnerHandler for registry/dispatch tests.
type stubHandler struct{ kind string }

func (s *stubHandler) Kind() string              { return s.kind }
func (s *stubHandler) WatchObjects() []WatchSpec { return nil }
func (s *stubHandler) TerminalState(context.Context, *v1alpha1.Reservation, SubjectReservations) (TerminalResult, error) {
	return TerminalResult{Done: true}, nil
}

func TestIdxReservationByOperationIDFn(t *testing.T) {
	tests := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{"wrong type", &hv1.Hypervisor{}, nil},
		{"non-inflight", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeCommittedResource}}, nil},
		{"no operation id", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
			Type:                v1alpha1.ReservationTypeInFlight,
			InFlightReservation: &v1alpha1.InFlightReservationSpec{},
		}}, nil},
		{"operation id set", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
			Type:                v1alpha1.ReservationTypeInFlight,
			InFlightReservation: &v1alpha1.InFlightReservationSpec{OperationID: "op-1"},
		}}, []string{"op-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := indexReservationByOperationID(tt.obj)
			if len(got) != len(tt.want) || (len(got) == 1 && got[0] != tt.want[0]) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPredicateReservations(t *testing.T) {
	c := &Controller{}
	pred := c.predicateReservations()
	tests := []struct {
		name string
		obj  client.Object
		want bool
	}{
		{"wrong type", &hv1.Hypervisor{}, false},
		{"wrong reservation type", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeFailover, SchedulingDomain: v1alpha1.SchedulingDomainNova}}, false},
		{"wrong domain", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeInFlight, SchedulingDomain: v1alpha1.SchedulingDomainPods}}, false},
		{"in-flight nova", &v1alpha1.Reservation{Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeInFlight, SchedulingDomain: v1alpha1.SchedulingDomainNova}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pred.Create(event.CreateEvent{Object: tt.obj}); got != tt.want {
				t.Errorf("Create = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetupWithManager_RejectsNonMulticlusterClient(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Controller{Client: newTestClient(scheme)}
	if err := c.SetupWithManager(context.Background(), nil); err == nil {
		t.Fatal("expected error for non-multicluster client, got nil")
	}
}

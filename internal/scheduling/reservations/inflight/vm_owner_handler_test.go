// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newVM builds a VirtualMachine with the given name and optional current host
// and conditions.
func newVM(name, host string, conds ...metav1.Condition) *vmv1alpha1.VirtualMachine {
	vm := &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if host != "" {
		vm.Status.Host = &vmv1alpha1.HostRef{Name: host}
	}
	for _, c := range conds {
		meta.SetStatusCondition(&vm.Status.Conditions, c)
	}
	return vm
}

func cond(condType string, status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: condType, Status: status, Reason: reason}
}

// withNovaStatus sets the VM's Nova status (e.g. ACTIVE, BUILD, ERROR).
func withNovaStatus(vm *vmv1alpha1.VirtualMachine, status string) *vmv1alpha1.VirtualMachine {
	vm.Status.Nova = &vmv1alpha1.NovaStatus{Status: status}
	return vm
}

// vmWithCond builds a VM carrying a single condition with an explicit
// LastTransitionTime, so freshness (ltt vs the reservation's creationTimestamp)
// can be controlled by the caller.
func vmWithCond(name, condType, reason string, ltt metav1.Time) *vmv1alpha1.VirtualMachine {
	vm := newVM(name, "host-1")
	vm.Status.Conditions = []metav1.Condition{{Type: condType, Status: metav1.ConditionFalse, Reason: reason, LastTransitionTime: ltt}}
	return vm
}

// newHandler wires a VirtualMachineHandler against a fake client.
func newHandler(t *testing.T, objects ...client.Object) *VirtualMachineHandler {
	t.Helper()
	k8sClient := newTestClient(newTestScheme(t), objects...)
	return &VirtualMachineHandler{Client: k8sClient}
}

// vmSiblings builds a SubjectReservations lookup that returns the VM-handled
// reservations for a subject: those with no owner reference or a VirtualMachine
// owner reference. This mirrors the ownership filtering the controller applies
// before calling TerminalState.
func vmSiblings(h *VirtualMachineHandler) SubjectReservations {
	return func(ctx context.Context, vmID string) ([]v1alpha1.Reservation, error) {
		list := &v1alpha1.ReservationList{}
		if err := h.List(ctx, list, client.MatchingFields{idxReservationByVMID: vmID}); err != nil {
			return nil, err
		}
		var out []v1alpha1.Reservation
		for i := range list.Items {
			res := &list.Items[i]
			vmOwned := len(res.OwnerReferences) == 0
			for _, ref := range res.OwnerReferences {
				if ref.Kind == "VirtualMachine" {
					vmOwned = true
				}
			}
			if vmOwned {
				out = append(out, *res)
			}
		}
		return out, nil
	}
}

func assertTerminal(t *testing.T, h *VirtualMachineHandler, res *v1alpha1.Reservation, wantDone bool, wantRequeue time.Duration) {
	t.Helper()
	result, err := h.TerminalState(context.Background(), res, vmSiblings(h))
	if err != nil {
		t.Fatalf("TerminalState error: %v", err)
	}
	if result.Done != wantDone {
		t.Errorf("done = %v, want %v", result.Done, wantDone)
	}
	if result.RequeueAfter != wantRequeue {
		t.Errorf("requeueAfter = %v, want %v", result.RequeueAfter, wantRequeue)
	}
}

func TestVMHandler_KindAndWatchObjects(t *testing.T) {
	h := &VirtualMachineHandler{}
	if h.Kind() != "VirtualMachine" {
		t.Error("Kind() must be VirtualMachine")
	}
	specs := h.WatchObjects()
	if len(specs) != 1 {
		t.Fatalf("expected 1 watch spec, got %d", len(specs))
	}
	if _, ok := specs[0].Object.(*vmv1alpha1.VirtualMachine); !ok {
		t.Error("watch should be VirtualMachine")
	}
}

func TestVMHandler_TerminalState(t *testing.T) {
	created := metav1.NewTime(time.Now().Truncate(time.Second))
	stale := metav1.NewTime(created.Add(-time.Hour))
	// withCond sets a single condition directly so LastTransitionTime is controlled.
	withCond := func(condType, reason string, ltt metav1.Time) *vmv1alpha1.VirtualMachine {
		return vmWithCond("vm-1", condType, reason, ltt)
	}
	type tc struct {
		name     string
		intent   v1alpha1.SchedulingIntent
		created  *metav1.Time
		vm       *vmv1alpha1.VirtualMachine // nil: no VM CRD -> wait (no terminal signal)
		labels   map[string]string
		wantDone bool
	}
	tests := []tc{
		// UC1 initial placement.
		{name: "create: building -> wait", intent: novaapi.CreateIntent,
			vm: newVM("vm-1", "host-9", cond(vmv1alpha1.ConditionTypeScheduled, metav1.ConditionUnknown, "InitialPlacement"))},
		{name: "create: Running=True -> done", intent: novaapi.CreateIntent, wantDone: true,
			vm: newVM("vm-1", "host-9", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running"))},
		{name: "create: nova ACTIVE -> done", intent: novaapi.CreateIntent, wantDone: true, vm: withNovaStatus(newVM("vm-1", "host-9"), "ACTIVE")},
		{name: "create: no live signal -> wait", intent: novaapi.CreateIntent, vm: withNovaStatus(newVM("vm-1", "host-9"), "BUILD")},
		{name: "create: scheduled false alone -> wait", intent: novaapi.CreateIntent,
			vm: newVM("vm-1", "", cond(vmv1alpha1.ConditionTypeScheduled, metav1.ConditionFalse, "NoHostFound"))},
		{name: "create: nova error -> done", intent: novaapi.CreateIntent, wantDone: true, vm: withNovaStatus(newVM("vm-1", ""), "ERROR")},
		// UC2 resize / UC3 live migration ongoing.
		{name: "resize: ongoing -> wait", intent: novaapi.ResizeIntent,
			vm: newVM("vm-1", "host-1", cond(vmv1alpha1.ConditionTypeResizing, metav1.ConditionTrue, vmv1alpha1.ConditionReasonResizingOngoing))},
		{name: "live migration: ongoing -> wait", intent: novaapi.LiveMigrationIntent,
			vm: newVM("vm-1", "host-1", cond(vmv1alpha1.ConditionTypeMigrating, metav1.ConditionTrue, vmv1alpha1.ConditionReasonMigratingOngoing))},
		// No VM CRD -> no terminal signal, keep waiting for the VM CRD or TTL
		// (regardless of intent).
		{name: "no VM CRD: create -> wait", intent: novaapi.CreateIntent},
		{name: "no VM CRD: resize -> wait", intent: novaapi.ResizeIntent},
		{name: "no VM CRD: live migration -> wait", intent: novaapi.LiveMigrationIntent},
		{name: "no VM CRD: empty intent -> wait"},
	}
	// Unmanaged-source resize (VMware → KVM): no KVM source hold, so there is no
	// Resizing condition. Release on landing plus alive; never on failure.
	unmanaged := map[string]string{LabelInFlightSource: LabelInFlightSourceUnmanaged}
	tests = append(tests,
		tc{name: "unmanaged resize: landed and running -> done", intent: novaapi.ResizeIntent, labels: unmanaged, wantDone: true,
			vm: newVM("vm-1", "host-1", cond(vmv1alpha1.ConditionTypeRunning, metav1.ConditionTrue, "Running"))},
		tc{name: "unmanaged resize: landed and nova ACTIVE -> done", intent: novaapi.ResizeIntent, labels: unmanaged, wantDone: true,
			vm: withNovaStatus(newVM("vm-1", "host-1"), "ACTIVE")},
		tc{name: "unmanaged resize: placed but not running -> wait", intent: novaapi.ResizeIntent, labels: unmanaged,
			vm: withNovaStatus(newVM("vm-1", "host-1"), "BUILD")},
		tc{name: "unmanaged resize: nova ERROR -> wait", intent: novaapi.ResizeIntent, labels: unmanaged,
			vm: withNovaStatus(newVM("vm-1", "host-1"), "ERROR")},
	)
	for _, r := range []string{vmv1alpha1.ConditionReasonResizingCompleted, vmv1alpha1.ConditionReasonResizingReverted, vmv1alpha1.ConditionReasonResizingFailed} {
		tests = append(tests, tc{name: "resize: terminal " + r, intent: novaapi.ResizeIntent, wantDone: true,
			vm: newVM("vm-1", "host-1", cond(vmv1alpha1.ConditionTypeResizing, metav1.ConditionFalse, r))})
	}
	for _, r := range []string{vmv1alpha1.ConditionReasonMigratingCompleted, vmv1alpha1.ConditionReasonMigratingRolledBack,
		vmv1alpha1.ConditionReasonMigratingAborting, vmv1alpha1.ConditionReasonMigratingFailed} {
		tests = append(tests, tc{name: "live migration: terminal " + r, intent: novaapi.LiveMigrationIntent, wantDone: true,
			vm: newVM("vm-1", "host-1", cond(vmv1alpha1.ConditionTypeMigrating, metav1.ConditionFalse, r))})
	}
	// A terminal condition left over from a previous operation (transition before
	// the reservation was created, or unknown) must not release fresh holds.
	for _, op := range []struct {
		name             string
		intent           v1alpha1.SchedulingIntent
		condType, reason string
	}{
		{"resize", novaapi.ResizeIntent, vmv1alpha1.ConditionTypeResizing, vmv1alpha1.ConditionReasonResizingCompleted},
		{"live_migration", novaapi.LiveMigrationIntent, vmv1alpha1.ConditionTypeMigrating, vmv1alpha1.ConditionReasonMigratingCompleted},
	} {
		tests = append(tests,
			tc{name: "stale " + op.name + " -> wait", intent: op.intent, created: &created, vm: withCond(op.condType, op.reason, stale)},
			tc{name: "fresh " + op.name + " -> done", intent: op.intent, created: &created, vm: withCond(op.condType, op.reason, created), wantDone: true},
			tc{name: "missing transition time " + op.name + " -> wait", intent: op.intent, created: &created, vm: withCond(op.condType, op.reason, metav1.Time{})},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := newInFlightReservation("r", "vm-1", "host-1", inFlightOpts{intent: tt.intent, labels: tt.labels})
			if tt.created != nil {
				res.CreationTimestamp = *tt.created
			}
			var objs []client.Object
			if tt.vm != nil {
				objs = append(objs, tt.vm)
			}
			wantRequeue := requeueInterval
			if tt.wantDone {
				wantRequeue = 0
			}
			assertTerminal(t, newHandler(t, objs...), res, tt.wantDone, wantRequeue)
		})
	}
}

func TestVMHandler_Supersession(t *testing.T) {
	now := time.Now()
	older := metav1.NewTime(now.Add(-time.Hour))
	newer := metav1.NewTime(now)
	mk := func(name, host, opID string, ts metav1.Time, ownerKind string) *v1alpha1.Reservation {
		r := newInFlightReservation(name, "vm-1", host, inFlightOpts{operationID: opID, intent: novaapi.CreateIntent})
		r.CreationTimestamp = ts
		if ownerKind != "" {
			r.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind}}
		}
		return r
	}
	// No VM CRD and no landing in all cases: without supersession everything waits.
	tests := []struct {
		name     string
		objects  []*v1alpha1.Reservation
		wantDone map[string]bool // checked reservation -> expected Done
	}{
		{
			name:     "older operation superseded by newer",
			objects:  []*v1alpha1.Reservation{mk("old", "host-1", "op-old", older, ""), mk("new", "host-2", "op-new", newer, "")},
			wantDone: map[string]bool{"old": true, "new": false},
		},
		{
			// Ownership is isolated even though the vmID subject is shared.
			name:     "ignores other owner kinds",
			objects:  []*v1alpha1.Reservation{mk("vm-op", "host-1", "op-vm", older, ""), mk("resched-op", "host-2", "op-resched", newer, "Rescheduling")},
			wantDone: map[string]bool{"vm-op": false},
		},
		{
			// Same creation second: UUIDv7 OperationID breaks the tie lexically.
			name:     "timestamp tie broken by operation ID",
			objects:  []*v1alpha1.Reservation{mk("lo", "host-1", "op-aaa", newer, ""), mk("hi", "host-2", "op-bbb", newer, "")},
			wantDone: map[string]bool{"lo": true, "hi": false},
		},
		{
			// Without an OperationID reservations cannot be ordered.
			name:     "no supersession without operation ID",
			objects:  []*v1alpha1.Reservation{mk("a", "host-1", "", metav1.Time{}, ""), mk("b", "host-2", "", metav1.Time{}, "")},
			wantDone: map[string]bool{"a": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]client.Object, len(tt.objects))
			for i, o := range tt.objects {
				objs[i] = o
			}
			h := newHandler(t, objs...)
			for name, wantDone := range tt.wantDone {
				// Read back so timestamps match the API-truncated values the handler lists.
				stored := &v1alpha1.Reservation{}
				if err := h.Get(context.Background(), client.ObjectKey{Name: name}, stored); err != nil {
					t.Fatal(err)
				}
				wantRequeue := requeueInterval
				if wantDone {
					wantRequeue = 0
				}
				assertTerminal(t, h, stored, wantDone, wantRequeue)
			}
		})
	}
}

func TestVMHandler_MapVMToReservations(t *testing.T) {
	matching := newInFlightReservation("r1", "vm-1", "host-1", inFlightOpts{})
	other := newInFlightReservation("r2", "vm-2", "host-2", inFlightOpts{})
	h := newHandler(t, matching, other)

	reqs := h.mapVMToReservations(context.Background(), newVM("vm-2", ""))
	if len(reqs) != 1 || reqs[0].Name != "r2" {
		t.Errorf("requests = %+v, want only r2", reqs)
	}
}

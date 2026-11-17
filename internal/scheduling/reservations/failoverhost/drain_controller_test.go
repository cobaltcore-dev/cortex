// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// drainConfig returns a DrainConfig with its defaults applied (24h TTL, 1m backoff).
func drainConfig() DrainConfig {
	c := DrainConfig{Enabled: true}
	c.ApplyDefaults()
	return c
}

// readyHV builds a ready hypervisor named hv1.
func readyHV() *hv1.Hypervisor {
	hv := buildHV("hv1", 64, 32)
	meta.SetStatusCondition(&hv.Status.Conditions, metav1.Condition{
		Type:   hv1.ConditionTypeReady,
		Status: metav1.ConditionTrue,
		Reason: hv1.ConditionReasonReadyReady,
	})
	return hv
}

// buildVM builds a VirtualMachine Nova reports on the given host with the given
// Nova status (e.g. "ACTIVE", "DELETED").
func buildVM(name, host, novaStatus string) *vmv1alpha1.VirtualMachine {
	return &vmv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     vmv1alpha1.VMStatus{Nova: &vmv1alpha1.NovaStatus{Host: host, Status: novaStatus}},
	}
}

// drainRescheduling builds a drain Rescheduling (as the controller creates them)
// targeting host, in the given state.
func drainRescheduling(name, host string, state v1alpha1.ReschedulingState) *v1alpha1.Rescheduling {
	return &v1alpha1.Rescheduling{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{v1alpha1.LabelFailoverHostDrain: "true", v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost, labelDrainReservationUID: "uid-" + host},
		},
		Spec: v1alpha1.ReschedulingSpec{
			Action: v1alpha1.ReschedulingActionDrain,
			Drain: &v1alpha1.DrainActionSpec{
				Reserve: true,
				Target:  v1alpha1.DrainTarget{Hosts: []v1alpha1.DrainTargetHost{{Host: v1alpha1.HostRef{Name: host}}}},
			},
		},
		Status: v1alpha1.ReschedulingStatus{State: state},
	}
}

// freshReservation builds a failover host reservation for hv1 (not yet Active)
// created just now, so the TTL does not fire.
func freshReservation() *v1alpha1.Reservation {
	r := failoverReservation("hv1")
	r.UID = "uid-hv1"
	r.Spec.FailoverHostReservation = &v1alpha1.FailoverHostReservationSpec{SourceHosts: []string{"peer"}}
	r.CreationTimestamp = metav1.Now()
	return &r
}

func getDrainReservation(t *testing.T, c client.Client, name string) *v1alpha1.Reservation {
	t.Helper()
	res := &v1alpha1.Reservation{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, res); err != nil {
		t.Fatalf("get reservation %q: %v", name, err)
	}
	return res
}

func listDrains(t *testing.T, c client.Client) []v1alpha1.Rescheduling {
	t.Helper()
	list := &v1alpha1.ReschedulingList{}
	if err := c.List(context.Background(), list); err != nil {
		t.Fatalf("list reschedulings: %v", err)
	}
	return list.Items
}

func TestDrainReconcile(t *testing.T) {
	terminating := buildVM("vm-terminating", "hv1", "ACTIVE")
	now := metav1.Now()
	terminating.Status.DeletionTime = &now
	expired := freshReservation()
	expired.CreationTimestamp = metav1.NewTime(time.Now().Add(-25 * time.Hour))
	deleting := freshReservation()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"test.io/hold"}
	deletingDrain := drainRescheduling("failover-host-hv1", "hv1", v1alpha1.ReschedulingStateProcessing)
	deletingDrain.DeletionTimestamp = &now
	deletingDrain.Finalizers = []string{"test.io/hold"}
	active := freshReservation()
	meta.SetStatusCondition(&active.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
	})
	activeInUse := freshReservation()
	meta.SetStatusCondition(&activeInUse.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
	})
	meta.SetStatusCondition(&activeInUse.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostInUse, Status: metav1.ConditionTrue, Reason: "Evacuating",
	})
	inFlight := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "in-flight-1"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeInFlight, TargetHost: "hv1"},
	}
	occupant := func() client.Object { return buildVM("vm1", "hv1", "ACTIVE") }
	drain := func(host string, state v1alpha1.ReschedulingState) client.Object {
		return drainRescheduling("failover-host-"+host, host, state)
	}

	const (
		requeueNone    = "none"
		requeueBackoff = "backoff"
		requeueTTL     = "ttl"
	)
	tests := []struct {
		name          string
		res           *v1alpha1.Reservation
		objs          []client.Object
		wantDeleted   bool
		wantActive    bool
		wantHV1Drains int
		wantRequeue   string
	}{
		{name: "hypervisor gone deletes", res: freshReservation(), wantDeleted: true},
		{name: "unready hypervisor is not deleted", res: freshReservation(),
			objs: []client.Object{buildHV("hv1", 64, 32), occupant()}, wantHV1Drains: 1},
		{name: "empty host goes active", res: freshReservation(),
			objs: []client.Object{readyHV()}, wantActive: true},
		{name: "deleted, terminating and foreign VMs do not occupy", res: freshReservation(),
			objs:       []client.Object{readyHV(), terminating, buildVM("vm2", "hv1", novaStatusDeleted), buildVM("vm3", "hv2", "ACTIVE")},
			wantActive: true},
		{name: "occupied host gets a drain", res: freshReservation(),
			objs: []client.Object{readyHV(), occupant()}, wantHV1Drains: 1},
		{name: "waits while another drain holds the slot", res: freshReservation(),
			objs:        []client.Object{readyHV(), occupant(), drain("hv2", v1alpha1.ReschedulingStateProcessing)},
			wantRequeue: requeueTTL},
		{name: "own drain in flight waits", res: freshReservation(),
			objs:          []client.Object{readyHV(), occupant(), drain("hv1", v1alpha1.ReschedulingStateProcessing)},
			wantHV1Drains: 1, wantRequeue: requeueTTL},
		{name: "succeeded drain and empty host goes active", res: freshReservation(),
			objs: []client.Object{readyHV(), drain("hv1", v1alpha1.ReschedulingStateSucceeded)}, wantActive: true, wantHV1Drains: 1},
		{name: "succeeded drain but occupied stays designated", res: freshReservation(),
			objs:          []client.Object{readyHV(), occupant(), drain("hv1", v1alpha1.ReschedulingStateSucceeded)},
			wantHV1Drains: 1, wantRequeue: requeueTTL},
		{name: "failed drain backs off", res: freshReservation(),
			objs:          []client.Object{readyHV(), occupant(), drain("hv1", v1alpha1.ReschedulingStateFailed)},
			wantHV1Drains: 1, wantRequeue: requeueBackoff},
		{name: "rejected drain backs off", res: freshReservation(),
			objs:          []client.Object{readyHV(), occupant(), drain("hv1", v1alpha1.ReschedulingStateRejected)},
			wantHV1Drains: 1, wantRequeue: requeueBackoff},
		{name: "another failed drain holds the slot", res: freshReservation(),
			objs:        []client.Object{readyHV(), occupant(), drain("hv2", v1alpha1.ReschedulingStateFailed)},
			wantRequeue: requeueTTL},
		{name: "another succeeded drain frees the slot", res: freshReservation(),
			objs: []client.Object{readyHV(), occupant(), drain("hv2", v1alpha1.ReschedulingStateSucceeded)}, wantHV1Drains: 1},
		{name: "TTL expiry deletes", res: expired,
			objs: []client.Object{readyHV(), occupant()}, wantDeleted: true},
		{name: "deleting reservation does not create a drain", res: deleting,
			objs: []client.Object{readyHV(), occupant()}},
		{name: "deleting drain is not replaced until removed", res: freshReservation(),
			objs: []client.Object{readyHV(), occupant(), deletingDrain}, wantHV1Drains: 1, wantRequeue: requeueBackoff},
		{name: "active with empty host stays active", res: active,
			objs: []client.Object{readyHV()}, wantActive: true},
		{name: "active but reoccupied stays active (no re-drain)", res: active,
			objs: []client.Object{readyHV(), occupant()}, wantActive: true},
		{name: "active and in use is sticky with VMs present", res: activeInUse,
			objs: []client.Object{readyHV(), occupant()}, wantActive: true},
		{name: "non failover host reservation is ignored", res: inFlight,
			objs: []client.Object{readyHV(), occupant()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			c := &DrainController{Client: newTestClient(t, append(tt.objs, tt.res)...), Config: drainConfig()}
			result, err := c.Reconcile(ctx, reconcileReq(tt.res.Name))
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			got := &v1alpha1.Reservation{}
			err = c.Get(ctx, client.ObjectKey{Name: tt.res.Name}, got)
			if deleted := apierrors.IsNotFound(err); deleted != tt.wantDeleted {
				t.Fatalf("deleted = %v, want %v (err=%v)", deleted, tt.wantDeleted, err)
			}
			if !tt.wantDeleted {
				if active := meta.IsStatusConditionTrue(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive); active != tt.wantActive {
					t.Errorf("active = %v, want %v", active, tt.wantActive)
				}
			}
			hv1Drains := 0
			for _, d := range listDrains(t, c) {
				if d.Spec.Drain != nil && d.Spec.Drain.Target.Hosts[0].Host.Name == "hv1" {
					hv1Drains++
				}
			}
			if hv1Drains != tt.wantHV1Drains {
				t.Errorf("drains for hv1 = %d, want %d", hv1Drains, tt.wantHV1Drains)
			}

			switch requeue := result.RequeueAfter; tt.wantRequeue {
			case requeueBackoff:
				if requeue != c.Config.RetryBackoff.Duration {
					t.Errorf("requeueAfter = %v, want %v", requeue, c.Config.RetryBackoff.Duration)
				}
			case requeueTTL:
				if requeue <= 0 || requeue > c.Config.TTL.Duration {
					t.Errorf("requeueAfter = %v, want within (0, %v]", requeue, c.Config.TTL.Duration)
				}
			default:
				if requeue != 0 {
					t.Errorf("requeueAfter = %v, want none", requeue)
				}
			}
		})
	}
}

func TestDrainCreatesRescheduling(t *testing.T) {
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	drains := listDrains(t, c)
	if len(drains) != 1 {
		t.Fatalf("expected exactly one drain, got %d", len(drains))
	}
	d := drains[0]
	switch {
	case d.Spec.Action != v1alpha1.ReschedulingActionDrain || d.Spec.Drain == nil:
		t.Fatalf("expected a drain-action rescheduling")
	case !d.Spec.Drain.Reserve:
		t.Fatalf("expected Reserve=true")
	case d.Spec.Drain.Constraints.Mode != v1alpha1.DrainConstraintModeAll:
		t.Fatalf("expected Mode=all, got %q", d.Spec.Drain.Constraints.Mode)
	case d.Labels[v1alpha1.LabelFailoverHostDrain] != "true":
		t.Fatalf("expected drain label, got %v", d.Labels)
	case d.Spec.Drain.Target.Hosts[0].Host.Name != "hv1":
		t.Fatalf("expected drain to target hv1")
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if d.Name != res.Name || d.GenerateName != "" {
		t.Fatalf("expected reservation name %q without GenerateName, got %q / %q", res.Name, d.Name, d.GenerateName)
	}
	if d.Labels[labelDrainReservationUID] != string(res.UID) {
		t.Fatalf("expected reservation UID label %q, got %v", res.UID, d.Labels)
	}
	if len(d.OwnerReferences) != 0 {
		t.Fatalf("expected no cross-cluster owner references, got %v", d.OwnerReferences)
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("repeat reconcile: %v", err)
	}
	if drains := listDrains(t, c); len(drains) != 1 {
		t.Fatalf("expected one drain after repeat reconcile, got %d", len(drains))
	}
}

func TestDrainRetriesAfterRemoval(t *testing.T) {
	for _, state := range []v1alpha1.ReschedulingState{v1alpha1.ReschedulingStateFailed, v1alpha1.ReschedulingStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			res := freshReservation()
			drain := drainRescheduling(res.Name, "hv1", state)
			c := &DrainController{Client: newTestClient(t, readyHV(), res, buildVM("vm1", "hv1", "ACTIVE"), drain), Config: drainConfig()}
			if _, err := c.Reconcile(ctx, reconcileReq(res.Name)); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if drains := listDrains(t, c); len(drains) != 1 || drains[0].Status.State != state {
				t.Fatalf("expected original drain to remain, got %+v", drains)
			}
			if err := c.Delete(ctx, drain); err != nil {
				t.Fatalf("delete drain: %v", err)
			}
			if _, err := c.Reconcile(ctx, reconcileReq(res.Name)); err != nil {
				t.Fatalf("retry: %v", err)
			}
			drains := listDrains(t, c)
			if len(drains) != 1 || drains[0].Name != res.Name || drains[0].Status.State != "" || drains[0].Labels[labelDrainReservationUID] != string(res.UID) {
				t.Fatalf("expected one fresh drain for reservation, got %+v", drains)
			}
		})
	}
}

func TestDrainDoesNotAdoptOldReservationDrain(t *testing.T) {
	for _, state := range []v1alpha1.ReschedulingState{v1alpha1.ReschedulingStateProcessing, v1alpha1.ReschedulingStateFailed, v1alpha1.ReschedulingStateSucceeded, v1alpha1.ReschedulingStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			res := freshReservation()
			drain := drainRescheduling(res.Name, "hv1", state)
			drain.Labels[labelDrainReservationUID] = "old-reservation"
			c := &DrainController{Client: newTestClient(t, readyHV(), res, buildVM("vm1", "hv1", "ACTIVE"), drain), Config: drainConfig()}
			result, err := c.Reconcile(ctx, reconcileReq(res.Name))
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if result.RequeueAfter <= 0 {
				t.Fatal("expected requeue while old drain exists")
			}
			drains := listDrains(t, c)
			if state == v1alpha1.ReschedulingStateSucceeded || state == v1alpha1.ReschedulingStateRejected {
				if len(drains) != 0 {
					t.Fatalf("expected stale terminal drain removed, got %+v", drains)
				}
			} else {
				if len(drains) != 1 || drains[0].Labels[labelDrainReservationUID] != "old-reservation" || drains[0].Status.State != state {
					t.Fatalf("expected old drain unchanged until cleanup, got %+v", drains)
				}
				if err := c.Delete(ctx, drain); err != nil {
					t.Fatalf("delete old drain after cleanup: %v", err)
				}
			}
			if _, err := c.Reconcile(ctx, reconcileReq(res.Name)); err != nil {
				t.Fatalf("reconcile after old drain removal: %v", err)
			}
			drains = listDrains(t, c)
			if len(drains) != 1 || drains[0].Labels[labelDrainReservationUID] != string(res.UID) {
				t.Fatalf("expected one drain for new reservation, got %+v", drains)
			}
		})
	}
}

func TestDrainNameConflict(t *testing.T) {
	ctx := context.Background()
	res := freshReservation()
	drain := drainRescheduling(res.Name, "hv2", v1alpha1.ReschedulingStateSucceeded)
	drain.Labels = map[string]string{v1alpha1.LabelFailoverHostDrain: "true"}
	c := &DrainController{Client: newTestClient(t, readyHV(), res, buildVM("vm1", "hv1", "ACTIVE"), drain), Config: drainConfig()}
	result, err := c.Reconcile(ctx, reconcileReq(res.Name))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != c.Config.RetryBackoff.Duration {
		t.Fatalf("expected backoff for name conflict, got %v", result.RequeueAfter)
	}
	drains := listDrains(t, c)
	if len(drains) != 1 || drains[0].Spec.Drain.Target.Hosts[0].Host.Name != "hv2" || drains[0].Labels[labelDrainReservationUID] != "" {
		t.Fatalf("expected unrelated rescheduling unchanged, got %+v", drains)
	}
}

// TestDrainEndToEnd walks the full happy path for an occupied host: a drain is
// created; once the drain succeeds and the VM is gone, the reservation goes Active.
func TestDrainEndToEnd(t *testing.T) {
	ctx := context.Background()
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	req := reconcileReq("failover-host-hv1")

	// Pass 1: host occupied -> a drain is created, reservation not yet Active.
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	drains := listDrains(t, c)
	if len(drains) != 1 {
		t.Fatalf("pass 1: expected one drain, got %d", len(drains))
	}

	// Simulate the rescheduler: mark the drain Succeeded and remove the VM.
	d := &drains[0]
	d.Status.State = v1alpha1.ReschedulingStateSucceeded
	if err := c.Status().Update(ctx, d); err != nil {
		t.Fatalf("update drain status: %v", err)
	}
	if err := c.Delete(ctx, buildVM("vm1", "hv1", "ACTIVE")); err != nil {
		t.Fatalf("delete vm: %v", err)
	}

	// Pass 2: drain succeeded and host empty -> reservation Active.
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("end-to-end: expected reservation Active after successful drain")
	}
}

// TestDrainReoccupiedStaysActive covers the invariant-violation path: a VM lands
// on an Active, not-in-use failover host (the filter should have blocked it). The
// controller must NOT re-drain it — re-draining could migrate a genuine evacuee
// off its rescue host on a stale in-use read. The reoccupation is surfaced by the
// failover_host_lifecycle KPI instead (tested there), not by the controller.
func TestDrainReoccupiedStaysActive(t *testing.T) {
	ctx := context.Background()
	active := freshReservation()
	meta.SetStatusCondition(&active.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
	})
	c := &DrainController{
		Client: newTestClient(t, readyHV(), active, buildVM("squatter", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(ctx, reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("expected reservation to stay Active, got %+v",
			meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive))
	}
	if drains := listDrains(t, c); len(drains) != 0 {
		t.Fatalf("expected no drain created, got %d", len(drains))
	}
}

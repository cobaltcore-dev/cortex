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
			Labels: map[string]string{LabelFailoverHostDrain: "true", v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost},
		},
		Spec: v1alpha1.ReschedulingSpec{
			Action: v1alpha1.ReschedulingActionDrain,
			Drain: &v1alpha1.DrainActionSpec{
				Target: v1alpha1.DrainTarget{Hosts: []v1alpha1.DrainTargetHost{{Host: v1alpha1.HostRef{Name: host}}}},
			},
		},
		Status: v1alpha1.ReschedulingStatus{State: state},
	}
}

// freshReservation builds a failover host reservation for hv1 (not yet Active)
// created just now, so the TTL does not fire.
func freshReservation() *v1alpha1.Reservation {
	r := failoverReservation("hv1")
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

func TestDrainDeletesWhenHypervisorGone(t *testing.T) {
	res := freshReservation() // no hypervisor object in the cluster.
	c := &DrainController{Client: newTestClient(t, res), Config: drainConfig()}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	err := c.Get(context.Background(), client.ObjectKey{Name: res.Name}, &v1alpha1.Reservation{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected reservation deleted, got err=%v", err)
	}
}

func TestDrainDoesNotDeleteWhenHypervisorUnready(t *testing.T) {
	hv := buildHV("hv1", 64, 32) // present but no Ready condition.
	res := freshReservation()
	vm := buildVM("vm1", "hv1", "ACTIVE")
	c := &DrainController{Client: newTestClient(t, hv, res, vm), Config: drainConfig()}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// Reservation must survive (unready != gone); and since the host is occupied a
	// drain should have been created.
	getDrainReservation(t, c, res.Name)
	if len(listDrains(t, c)) != 1 {
		t.Fatalf("expected a drain to be created for the occupied host")
	}
}

func TestDrainMarksActiveWhenHostEmpty(t *testing.T) {
	c := &DrainController{Client: newTestClient(t, readyHV(), freshReservation()), Config: drainConfig()}
	if _, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("expected reservation to be marked Active")
	}
}

func TestDrainIgnoresDeletedAndTerminatingVMs(t *testing.T) {
	deleting := buildVM("vm1", "hv1", "ACTIVE")
	now := metav1.Now()
	deleting.Status.DeletionTime = &now
	gone := buildVM("vm2", "hv1", novaStatusDeleted)
	elsewhere := buildVM("vm3", "hv2", "ACTIVE")
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), deleting, gone, elsewhere),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("expected reservation Active: only non-present VMs are on the host")
	}
}

func TestDrainCreatesReschedulingForOccupiedHost(t *testing.T) {
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
	if d.Spec.Action != v1alpha1.ReschedulingActionDrain || d.Spec.Drain == nil {
		t.Fatalf("expected a drain-action rescheduling")
	}
	if d.Spec.Drain.Reserve {
		t.Fatalf("expected Reserve=false")
	}
	if d.Spec.Drain.Constraints.Mode != v1alpha1.DrainConstraintModeAll {
		t.Fatalf("expected Mode=all, got %q", d.Spec.Drain.Constraints.Mode)
	}
	if !drainHostsContain(&d, "hv1") {
		t.Fatalf("expected drain to target hv1")
	}
	// Reservation must NOT be Active yet.
	res := getDrainReservation(t, c, "failover-host-hv1")
	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("reservation should not be Active while draining")
	}
}

func TestDrainSingleFlightWaitsForOtherDrain(t *testing.T) {
	// A drain for hv2 is already Processing; hv1 is occupied but must wait.
	other := drainRescheduling("drain-hv2", "hv2", v1alpha1.ReschedulingStateProcessing)
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), buildVM("vm1", "hv1", "ACTIVE"), other),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// No new drain for hv1 should have been created (still only the hv2 one).
	for _, d := range listDrains(t, c) {
		if drainHostsContain(&d, "hv1") {
			t.Fatalf("expected no drain for hv1 while another drain is in flight")
		}
	}
}

func TestDrainSucceededAndEmptyMarksActive(t *testing.T) {
	done := drainRescheduling("drain-hv1", "hv1", v1alpha1.ReschedulingStateSucceeded)
	// Host is now empty (no VMs).
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), done),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("expected Active after a succeeded drain emptied the host")
	}
}

func TestDrainSucceededButStillFullStaysDesignated(t *testing.T) {
	done := drainRescheduling("drain-hv1", "hv1", v1alpha1.ReschedulingStateSucceeded)
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), done, buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	result, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("expected no requeue when drain succeeded but host still full, got %v", result.RequeueAfter)
	}
	res := getDrainReservation(t, c, "failover-host-hv1")
	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("reservation must not be Active while the host is still occupied")
	}
	// No second drain should be created (the Succeeded one still targets the host).
	if n := len(listDrains(t, c)); n != 1 {
		t.Fatalf("expected no new drain, got %d drains", n)
	}
}

func TestDrainFailedBacksOff(t *testing.T) {
	failed := drainRescheduling("drain-hv1", "hv1", v1alpha1.ReschedulingStateFailed)
	c := &DrainController{
		Client: newTestClient(t, readyHV(), freshReservation(), failed, buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	result, err := c.Reconcile(context.Background(), reconcileReq("failover-host-hv1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != c.Config.RetryBackoff.Duration {
		t.Fatalf("expected requeue after %v, got %v", c.Config.RetryBackoff.Duration, result.RequeueAfter)
	}
}

func TestDrainTTLExpiryDeletes(t *testing.T) {
	res := freshReservation()
	res.CreationTimestamp = metav1.NewTime(time.Now().Add(-25 * time.Hour)) // older than the 24h TTL.
	c := &DrainController{
		Client: newTestClient(t, readyHV(), res, buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	err := c.Get(context.Background(), client.ObjectKey{Name: res.Name}, &v1alpha1.Reservation{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected reservation deleted at TTL, got err=%v", err)
	}
}

func TestDrainActiveReservationIsStickyWithVMsPresent(t *testing.T) {
	res := freshReservation()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: "HostDrained",
	})
	// An in-progress InUse evacuation has landed a VM on the (Active) host.
	c := &DrainController{
		Client: newTestClient(t, readyHV(), res, buildVM("vm1", "hv1", "ACTIVE")),
		Config: drainConfig(),
	}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := getDrainReservation(t, c, res.Name)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		t.Fatalf("Active must stay sticky even with VMs present")
	}
	if len(listDrains(t, c)) != 0 {
		t.Fatalf("must not drain an Active reservation")
	}
}

func TestDrainIgnoresNonFailoverHostReservation(t *testing.T) {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "in-flight-1"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeInFlight},
	}
	c := &DrainController{Client: newTestClient(t, res), Config: drainConfig()}
	if _, err := c.Reconcile(context.Background(), reconcileReq(res.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// Untouched: still present, no drains.
	getDrainReservation(t, c, res.Name)
	if len(listDrains(t, c)) != 0 {
		t.Fatalf("must not act on non-failover-host reservations")
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

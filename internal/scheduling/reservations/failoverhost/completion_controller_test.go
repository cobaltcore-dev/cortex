// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// completionConfig returns a CompletionConfig with its defaults applied (24h grace).
func completionConfig() CompletionConfig {
	c := CompletionConfig{Enabled: true}
	c.ApplyDefaults()
	return c
}

// inUseReservation builds a failover host reservation for hv1 that is Active and
// InUse with "peer" recorded as its source host.
func inUseReservation() *v1alpha1.Reservation {
	const sourceHost = "peer"
	r := failoverReservation("hv1")
	r.Spec.FailoverHostReservation = &v1alpha1.FailoverHostReservationSpec{SourceHosts: []string{sourceHost}}
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
	})
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostInUse, Status: metav1.ConditionTrue, Reason: "EvacuationStarted",
	})
	r.Status.FailoverHostReservation = &v1alpha1.FailoverHostReservationStatus{SourceHost: sourceHost}
	return &r
}

// withCompletedTrue stamps a FailoverHostCompleted=True condition at the given age.
func withCompletedTrue(r *v1alpha1.Reservation, age time.Duration) *v1alpha1.Reservation {
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionTypeFailoverHostCompleted, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostEvacuationComplete,
	})
	cond := meta.FindStatusCondition(r.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)
	cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-age))
	return r
}

func TestCompletionReconcile(t *testing.T) {
	notInUse := func() *v1alpha1.Reservation {
		r := failoverReservation("hv1")
		r.Spec.FailoverHostReservation = &v1alpha1.FailoverHostReservationSpec{SourceHosts: []string{"peer"}}
		meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
			Type: v1alpha1.ConditionTypeFailoverHostActive, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonFailoverHostDrained,
		})
		return &r
	}
	noSource := func() *v1alpha1.Reservation {
		r := inUseReservation()
		r.Status.FailoverHostReservation.SourceHost = ""
		return r
	}
	inFlight := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "in-flight-1"},
		Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeInFlight, TargetHost: "hv1"},
	}
	sourceOccupant := func() client.Object { return buildVM("evacuee", "peer", "ACTIVE") }

	const (
		requeueNone  = "none"
		requeueGrace = "grace"
	)
	tests := []struct {
		name          string
		res           *v1alpha1.Reservation
		objs          []client.Object
		wantDeleted   bool
		wantCompleted *metav1.ConditionStatus
		wantRequeue   string
	}{
		{name: "not in use is ignored", res: notInUse(), wantRequeue: requeueNone},
		{name: "in use without source host skips", res: noSource(), wantRequeue: requeueNone},
		{name: "source empty first time marks completed and requeues grace", res: inUseReservation(),
			wantCompleted: ptrStatus(metav1.ConditionTrue), wantRequeue: requeueGrace},
		{name: "source occupied does nothing", res: inUseReservation(),
			objs: []client.Object{sourceOccupant()}, wantRequeue: requeueNone},
		{name: "source re-occupied resets completed to false", res: withCompletedTrue(inUseReservation(), time.Hour),
			objs: []client.Object{sourceOccupant()}, wantCompleted: ptrStatus(metav1.ConditionFalse), wantRequeue: requeueNone},
		{name: "grace not elapsed waits", res: withCompletedTrue(inUseReservation(), time.Hour),
			wantCompleted: ptrStatus(metav1.ConditionTrue), wantRequeue: requeueGrace},
		{name: "grace elapsed deletes", res: withCompletedTrue(inUseReservation(), 25*time.Hour),
			wantDeleted: true},
		{name: "non failover host reservation is ignored", res: inFlight, wantRequeue: requeueNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			c := &CompletionController{Client: newTestClient(t, append(tt.objs, tt.res)...), Config: completionConfig()}
			result, err := c.Reconcile(ctx, reconcileReq(tt.res.Name))
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			got := &v1alpha1.Reservation{}
			err = c.Get(ctx, client.ObjectKey{Name: tt.res.Name}, got)
			if deleted := apierrors.IsNotFound(err); deleted != tt.wantDeleted {
				t.Fatalf("deleted = %v, want %v (err=%v)", deleted, tt.wantDeleted, err)
			}
			if tt.wantDeleted {
				return
			}
			cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)
			switch {
			case tt.wantCompleted == nil && cond != nil:
				t.Errorf("completed condition = %v, want absent", cond.Status)
			case tt.wantCompleted != nil && cond == nil:
				t.Errorf("completed condition absent, want %v", *tt.wantCompleted)
			case tt.wantCompleted != nil && cond.Status != *tt.wantCompleted:
				t.Errorf("completed condition = %v, want %v", cond.Status, *tt.wantCompleted)
			}

			switch requeue := result.RequeueAfter; tt.wantRequeue {
			case requeueGrace:
				if requeue <= 0 || requeue > c.Config.GracePeriod.Duration {
					t.Errorf("requeueAfter = %v, want within (0, %v]", requeue, c.Config.GracePeriod.Duration)
				}
			default:
				if requeue != 0 {
					t.Errorf("requeueAfter = %v, want none", requeue)
				}
			}
		})
	}
}

func TestCompletionEndToEnd(t *testing.T) {
	ctx := context.Background()
	res := inUseReservation()
	c := &CompletionController{
		Client: newTestClient(t, res, buildVM("evacuee", "peer", "ACTIVE")),
		Config: completionConfig(),
	}
	req := reconcileReq(res.Name)

	// Pass 1: VM still on the source host, nothing happens.
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	got := &v1alpha1.Reservation{}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get after pass 1: %v", err)
	}
	if meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted) != nil {
		t.Fatalf("did not expect a completed condition while source is occupied")
	}

	// Source VM leaves; pass 2 marks the reservation completed and starts the clock.
	if err := c.Delete(ctx, buildVM("evacuee", "peer", "ACTIVE")); err != nil {
		t.Fatalf("delete evacuee: %v", err)
	}
	result, err := c.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("expected a grace requeue, got %v", result.RequeueAfter)
	}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get after pass 2: %v", err)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted) {
		t.Fatalf("expected completed=true after source emptied")
	}

	// Back-date the condition beyond the grace period; pass 3 deletes.
	base := got.DeepCopy()
	cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)
	cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-25 * time.Hour))
	if err := c.Status().Patch(ctx, got, client.MergeFrom(base)); err != nil {
		t.Fatalf("back-date completed condition: %v", err)
	}
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}
	if err := c.Get(ctx, req.NamespacedName, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected reservation deleted after grace elapsed, got err=%v", err)
	}
}

func TestCompletionResetTimerOnReoccupation(t *testing.T) {
	ctx := context.Background()
	res := withCompletedTrue(inUseReservation(), time.Hour)
	original := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted).LastTransitionTime
	c := &CompletionController{
		Client: newTestClient(t, res, buildVM("returned", "peer", "ACTIVE")),
		Config: completionConfig(),
	}
	req := reconcileReq(res.Name)

	// Source re-occupied: completed flips to False, reservation not deleted.
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile reoccupied: %v", err)
	}
	got := &v1alpha1.Reservation{}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get after reoccupation: %v", err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != v1alpha1.ReasonFailoverHostSourceReoccupied {
		t.Fatalf("expected completed=false reason SourceReoccupied, got %+v", cond)
	}

	// VM leaves again: completed flips back to True with a later timestamp.
	if err := c.Delete(ctx, buildVM("returned", "peer", "ACTIVE")); err != nil {
		t.Fatalf("delete returned vm: %v", err)
	}
	if _, err := c.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile after re-empty: %v", err)
	}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get after re-empty: %v", err)
	}
	cond = meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected completed=true after re-empty, got %+v", cond)
	}
	if !cond.LastTransitionTime.After(original.Time) {
		t.Errorf("expected grace clock to restart (lastTransitionTime after %v), got %v", original.Time, cond.LastTransitionTime.Time)
	}
}

func ptrStatus(s metav1.ConditionStatus) *metav1.ConditionStatus { return &s }

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// LabelFailoverHostDrain marks the Rescheduling objects created by the drain
	// controller, so they can be found and counted without matching on names.
	LabelFailoverHostDrain = "reservations.cortex.cloud/failover-host-drain"

	// idxReschedulingByHost indexes drain Reschedulings by the hosts they target,
	// so the controller can cheaply find the drain (if any) for a reservation's
	// host and count drains that are in flight across the whole fleet.
	idxReschedulingByHost = "failoverhost.drain.targetHost"

	// drainReschedulingPrefix is the GenerateName prefix for the drain
	// Reschedulings this controller creates. A generated suffix is used (rather
	// than a deterministic name) because nothing prunes a Succeeded drain
	// Rescheduling, so a fixed name would clash with a lingering completed one on
	// the next attempt for the same host.
	drainReschedulingPrefix = "drain-failover-host-"

	// novaStatusDeleted is the Nova instance status reported for a server that has
	// been deleted. A virtual machine in this state no longer occupies its host.
	novaStatusDeleted = "DELETED"
)

// DrainController is the failover host lifecycle reconciler: it brings freshly
// designated failover host reservations (Reservations of type
// FailoverHostReservation without the FailoverHostActive condition) into an
// Active state by ensuring the reserved host is empty of virtual machines.
//
// It is the counterpart to the partitioning Controller in this package: that
// controller creates and prunes the reservation objects (writing only their spec
// and labels), while this one owns the reservation status — the Designated ->
// Active transition and the deletion of reservations that can no longer be made
// Active. The Matcher (matching.go) only ever advances a reservation Active ->
// InUse, and only ever selects Active reservations, so a Designated reservation
// is exclusively this controller's concern until it goes Active.
//
// A host that still runs virtual machines is drained by creating a Rescheduling
// with the drain action; the rescheduler live-migrates the workloads away. Only
// one such drain runs at a time across the whole fleet, and draining is gated by
// a feature flag that is off by default (see DrainConfig): the controller is not
// started at all when disabled.
type DrainController struct {
	client.Client

	// Config holds the TTL and retry backoff. Enabled gating happens before the
	// controller is constructed (in main.go), so by the time a DrainController
	// exists, draining is enabled.
	Config DrainConfig

	// Monitor records Prometheus metrics. It may be nil (e.g. in tests); every
	// call site nil-guards it.
	Monitor *Monitor
}

// Reconcile drives a single failover host reservation toward Active, draining its
// host if necessary. The branches, in order:
//
//  1. the hypervisor is gone              -> delete the reservation.
//  2. the reservation is already Active   -> nothing to do (Active is sticky).
//  3. the reservation is older than the TTL and still not Active -> delete it.
//  4. the host is already empty           -> mark the reservation Active.
//  5. the host is not empty               -> drive the drain Rescheduling:
//     create one (respecting the single-drain-at-a-time rule), wait for one in
//     flight, re-verify emptiness on Succeeded, or back off on Failed/Rejected.
//
// It is level-triggered and idempotent, and never demotes an Active reservation:
// once Active a reservation may legitimately gain inbound VMs from an in-progress
// gang evacuation (InUse), and flipping it back would break that evacuation.
func (c *DrainController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("failoverhost-drain-reconcile").
		WithValues("reservationName", req.Name)

	res := &v1alpha1.Reservation{}
	if err := c.Get(ctx, req.NamespacedName, res); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Sanity guards; the predicate already filters to failover host reservations.
	if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost || res.Spec.FailoverHostReservation == nil {
		return ctrl.Result{}, nil
	}
	host := res.Spec.TargetHost
	if host == "" {
		log.V(1).Info("failover host reservation has no target host, nothing to drain")
		return ctrl.Result{}, nil
	}

	// 1. The hypervisor is gone: the reservation can never be satisfied, so delete
	// it and let the partitioning generator designate a fresh one. An unready (but
	// still present) hypervisor is deliberately NOT a delete trigger: the generator
	// lists all hypervisors regardless of readiness, so deleting on unready would
	// race the generator recreating the reservation every generation interval. A
	// genuinely unschedulable host instead stops being designated by the generator,
	// and a reservation stuck non-Active is eventually cleaned up by the TTL below.
	hv := &hv1.Hypervisor{}
	if err := c.Get(ctx, client.ObjectKey{Name: host}, hv); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hypervisor gone, deleting failover host reservation", "host", host)
			return ctrl.Result{}, c.deleteReservation(ctx, res)
		}
		return ctrl.Result{}, err
	}

	// 2. Already Active: never demote. Removal when the host leaves a partition is
	// the partitioning controller's job; advancing Active -> InUse is the Matcher's.
	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		return ctrl.Result{}, nil
	}

	// 3. TTL: a reservation that has not reached Active within the (conservative)
	// TTL since creation is deleted so the generator can try a fresh host.
	if age := time.Since(res.CreationTimestamp.Time); age > c.Config.TTL.Duration {
		log.Info("failover host reservation exceeded drain TTL, deleting",
			"host", host, "age", age, "ttl", c.Config.TTL.Duration)
		return ctrl.Result{}, c.deleteReservation(ctx, res)
	}

	// 4. Host empty: mark the reservation Active.
	empty, err := c.hostEmpty(ctx, host)
	if err != nil {
		return ctrl.Result{}, err
	}
	if empty {
		log.Info("host is empty, marking failover host reservation active", "host", host)
		return ctrl.Result{}, c.markActive(ctx, res)
	}

	// 5. Host not empty: drive the drain.
	return c.drive(ctx, res, host)
}

// drive advances the drain for a reservation whose host is not empty. It finds the
// drain Rescheduling for this host (if any) and acts on its state.
func (c *DrainController) drive(ctx context.Context, res *v1alpha1.Reservation, host string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("failoverhost-drain-reconcile").WithValues("host", host)

	existing, err := c.drainForHost(ctx, host)
	if err != nil {
		return ctrl.Result{}, err
	}

	if existing == nil {
		// No drain for this host yet. Respect the single-drain-at-a-time rule: if
		// any drain is already in flight anywhere in the fleet, wait. The
		// Rescheduling watch re-triggers this reservation when that drain settles.
		inFlight, err := c.anotherDrainInFlight(ctx, host)
		if err != nil {
			return ctrl.Result{}, err
		}
		if inFlight {
			log.V(1).Info("another drain is in flight, waiting before draining this host")
			return ctrl.Result{}, nil
		}
		if err := c.createDrain(ctx, res, host); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("created drain rescheduling for failover host")
		return ctrl.Result{}, nil
	}

	switch drainState(existing) {
	case v1alpha1.ReschedulingStateSucceeded:
		// The rescheduler reports the drain done. Do not trust it blindly (it may
		// be a stub, or a best-effort drain): re-verify emptiness via the VM CRDs.
		empty, err := c.hostEmpty(ctx, host)
		if err != nil {
			return ctrl.Result{}, err
		}
		if empty {
			log.Info("drain succeeded and host is empty, marking failover host reservation active")
			return ctrl.Result{}, c.markActive(ctx, res)
		}
		// Succeeded but the host is still occupied. The Rescheduling is terminal and
		// nothing re-drives it, so do not requeue: the VM watch re-triggers us when
		// workloads actually leave, and the TTL deletes the reservation otherwise.
		log.Info("drain reported success but host still has virtual machines, leaving reservation designated")
		return ctrl.Result{}, nil
	case v1alpha1.ReschedulingStateFailed, v1alpha1.ReschedulingStateRejected:
		// The rescheduler owns cleaning up a failed request. Wait for it to prune
		// the object, then a fresh drain is created on a later reconcile.
		log.V(1).Info("drain rescheduling in terminal error state, backing off", "state", drainState(existing))
		return ctrl.Result{RequeueAfter: c.Config.RetryBackoff.Duration}, nil
	default:
		// Pending/Acknowledged/Processing/Accepted: in flight, wait for the watch.
		log.V(1).Info("drain rescheduling in flight, waiting", "state", drainState(existing))
		return ctrl.Result{}, nil
	}
}

// hostEmpty reports whether no live virtual machine runs on the given host, based
// on the vm-operator VirtualMachine resources. A VM counts as present only if Nova
// reports it on this host and it is neither being pruned nor already deleted.
func (c *DrainController) hostEmpty(ctx context.Context, host string) (bool, error) {
	vms := &vmv1alpha1.VirtualMachineList{}
	if err := c.List(ctx, vms); err != nil {
		return false, err
	}
	for _, vm := range vms.Items {
		if vm.Status.Nova == nil || vm.Status.Nova.Host != host {
			continue // not placed yet, or on another host.
		}
		if vm.Status.DeletionTime != nil || vm.Status.Nova.Status == novaStatusDeleted {
			continue // being pruned / already gone.
		}
		return false, nil
	}
	return true, nil
}

// markActive sets the FailoverHostActive condition to True on the reservation.
func (c *DrainController) markActive(ctx context.Context, res *v1alpha1.Reservation) error {
	old := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ConditionTypeFailoverHostActive,
		Status:  metav1.ConditionTrue,
		Reason:  "HostDrained",
		Message: "no virtual machines remain on the reserved host",
	})
	if err := c.Status().Patch(ctx, res, client.MergeFrom(old)); err != nil {
		c.Monitor.RecordReservationError("drain_mark_active")
		return err
	}
	return nil
}

// deleteReservation deletes a reservation, tolerating a concurrent delete.
func (c *DrainController) deleteReservation(ctx context.Context, res *v1alpha1.Reservation) error {
	if err := c.Delete(ctx, res); err != nil && !apierrors.IsNotFound(err) {
		c.Monitor.RecordReservationError("drain_delete")
		return err
	}
	c.Monitor.RecordReservationDeleted()
	return nil
}

// createDrain creates a drain Rescheduling for the reservation's host. It uses a
// generated name so repeated attempts never clash with a lingering Succeeded
// Rescheduling (which nothing prunes). Reserve is false: the failover host
// reservation and the pipeline filter already block the host from placement, so
// the rescheduler needs no independent host-block.
func (c *DrainController) createDrain(ctx context.Context, res *v1alpha1.Reservation, host string) error {
	rr := &v1alpha1.Rescheduling{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: drainReschedulingPrefix + host + "-",
			Labels: map[string]string{
				LabelFailoverHostDrain:        "true",
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost,
			},
		},
		Spec: v1alpha1.ReschedulingSpec{
			Action:           v1alpha1.ReschedulingActionDrain,
			AvailabilityZone: res.Spec.AvailabilityZone,
			Drain: &v1alpha1.DrainActionSpec{
				Reserve: false,
				Target: v1alpha1.DrainTarget{Hosts: []v1alpha1.DrainTargetHost{{
					Host: v1alpha1.HostRef{Name: host, AvailabilityZone: res.Spec.AvailabilityZone},
				}}},
				Constraints: v1alpha1.DrainConstraints{Mode: v1alpha1.DrainConstraintModeAll},
			},
		},
	}
	if err := c.Create(ctx, rr); err != nil {
		c.Monitor.RecordReservationError("drain_create")
		return err
	}
	return nil
}

// drainForHost returns the drain Rescheduling targeting the given host, or nil if
// none exists. A terminal, no-longer-occupying request (Succeeded whose work this
// controller has already consumed, or a pruned one) may still linger; the caller
// inspects the returned object's state. Succeeded/Rejected drains for the host
// that are no longer relevant are ignored here only in that drive() re-decides.
func (c *DrainController) drainForHost(ctx context.Context, host string) (*v1alpha1.Rescheduling, error) {
	list := &v1alpha1.ReschedulingList{}
	if err := c.List(ctx, list, client.MatchingFields{idxReschedulingByHost: host}); err != nil {
		return nil, err
	}
	// Prefer a non-terminal (in-flight) drain; otherwise fall back to a terminal
	// one so drive() can act on Succeeded (re-verify) or Failed (back off). A
	// lingering Succeeded drain that has already been consumed is harmless: the
	// host is either empty (reservation already Active, handled earlier) or still
	// occupied, in which case we must not create a second concurrent drain.
	var terminal *v1alpha1.Rescheduling
	for i := range list.Items {
		rr := &list.Items[i]
		if !isDrain(rr) {
			continue
		}
		if drainInFlight(drainState(rr)) {
			return rr, nil
		}
		if terminal == nil {
			terminal = rr
		}
	}
	return terminal, nil
}

// anotherDrainInFlight reports whether a drain Rescheduling is in flight for any
// host other than the given one. A Failed drain still counts as occupying the
// single-drain slot until the rescheduler prunes it, since it may hold partial
// state.
func (c *DrainController) anotherDrainInFlight(ctx context.Context, exceptHost string) (bool, error) {
	list := &v1alpha1.ReschedulingList{}
	if err := c.List(ctx, list, client.MatchingLabels{LabelFailoverHostDrain: "true"}); err != nil {
		return false, err
	}
	for i := range list.Items {
		rr := &list.Items[i]
		if !isDrain(rr) {
			continue
		}
		if drainTargetsHost(rr, exceptHost) {
			continue
		}
		if drainOccupiesSlot(drainState(rr)) {
			return true, nil
		}
	}
	return false, nil
}

// SetupWithManager wires the drain controller into the manager. It watches four
// resources and enqueues the affected failover host reservation(s):
//
//   - Reservation (the primary object) — filtered to failover host reservations.
//   - Hypervisor — so a host going away re-triggers its reservation.
//   - VirtualMachine — so workloads leaving (or arriving on) a host re-triggers
//     the reservation of that host.
//   - Rescheduling — so a drain changing state re-triggers the reservation(s) it
//     targets.
//
// Reconciles run one at a time (MaxConcurrentReconciles: 1), which both keeps the
// single-drain-at-a-time bookkeeping race-free and bounds the load from the full
// VM list the emptiness check performs.
func (c *DrainController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	// Shared reservation-by-host index, reused for the Hypervisor and VM watches.
	if err := reservations.IndexReservationByHost(ctx, mcl); err != nil {
		return err
	}
	// Index drain Reschedulings by the hosts they target.
	if err := mcl.IndexField(ctx,
		&v1alpha1.Rescheduling{},
		&v1alpha1.ReschedulingList{},
		idxReschedulingByHost,
		reschedulingHostsIndexFn,
	); err != nil {
		return err
	}

	bldr := multicluster.BuildController(mcl, mgr)
	var err error
	if bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		enqueueSelf(), failoverHostReservationPredicate()); err != nil {
		return err
	}
	if bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		c.enqueueReservationsForHost(func(obj client.Object) string { return obj.GetName() }),
		hypervisorPredicate()); err != nil {
		return err
	}
	if bldr, err = bldr.WatchesMulticluster(&vmv1alpha1.VirtualMachine{},
		c.enqueueReservationsForHost(vmHost), vmHostPredicate()); err != nil {
		return err
	}
	if bldr, err = bldr.WatchesMulticluster(&v1alpha1.Rescheduling{},
		c.enqueueReservationsForDrain(), drainReschedulingPredicate()); err != nil {
		return err
	}
	return bldr.Named("failoverhost-drain-controller").
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

// enqueueSelf enqueues a reconcile for the (cluster-scoped) object that changed.
func enqueueSelf() handler.EventHandler {
	h := handler.Funcs{}
	enqueue := func(name string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		q.Add(ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	}
	h.CreateFunc = func(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.Object.GetName(), q)
	}
	h.UpdateFunc = func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.ObjectNew.GetName(), q)
	}
	h.DeleteFunc = func(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.Object.GetName(), q)
	}
	return h
}

// enqueueReservationsForHost builds an event handler that maps an object to the
// host it concerns (via hostOf) and enqueues every failover host reservation on
// that host, using the shared reservation-by-host index.
func (c *DrainController) enqueueReservationsForHost(hostOf func(client.Object) string) handler.EventHandler {
	enqueue := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		host := hostOf(obj)
		if host == "" {
			return
		}
		list := &v1alpha1.ReservationList{}
		if err := c.List(ctx, list, client.MatchingFields{reservations.IdxReservationByHost: host}); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list reservations for host", "host", host)
			return
		}
		for i := range list.Items {
			res := &list.Items[i]
			if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
				continue
			}
			q.Add(ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}})
		}
	}
	h := handler.Funcs{}
	h.CreateFunc = func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(ctx, e.Object, q)
	}
	h.UpdateFunc = func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(ctx, e.ObjectNew, q)
	}
	h.DeleteFunc = func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(ctx, e.Object, q)
	}
	return h
}

// enqueueReservationsForDrain enqueues the failover host reservation(s) of the
// hosts a drain Rescheduling targets, so a drain changing state re-triggers them.
func (c *DrainController) enqueueReservationsForDrain() handler.EventHandler {
	enqueue := func(obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		rr, ok := obj.(*v1alpha1.Rescheduling)
		if !ok {
			return
		}
		for _, host := range drainHosts(rr) {
			q.Add(ctrl.Request{NamespacedName: types.NamespacedName{Name: "failover-host-" + host}})
		}
	}
	h := handler.Funcs{}
	h.CreateFunc = func(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.Object, q)
	}
	h.UpdateFunc = func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.ObjectNew, q)
	}
	h.DeleteFunc = func(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		enqueue(e.Object, q)
	}
	return h
}

// ---- predicates ----

func failoverHostReservationPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		res, ok := obj.(*v1alpha1.Reservation)
		return ok && res.Spec.Type == v1alpha1.ReservationTypeFailoverHost
	})
}

func hypervisorPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		_, ok := obj.(*hv1.Hypervisor)
		return ok
	})
}

// vmHostPredicate fires only when a VM's observed host or deletion state changes,
// the only transitions that affect whether a host is empty.
func vmHostPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { _, ok := e.Object.(*vmv1alpha1.VirtualMachine); return ok },
		DeleteFunc:  func(e event.DeleteEvent) bool { _, ok := e.Object.(*vmv1alpha1.VirtualMachine); return ok },
		GenericFunc: func(e event.GenericEvent) bool { _, ok := e.Object.(*vmv1alpha1.VirtualMachine); return ok },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldVM, ok1 := e.ObjectOld.(*vmv1alpha1.VirtualMachine)
			newVM, ok2 := e.ObjectNew.(*vmv1alpha1.VirtualMachine)
			if !ok1 || !ok2 {
				return false
			}
			return vmHost(oldVM) != vmHost(newVM) ||
				(oldVM.Status.DeletionTime == nil) != (newVM.Status.DeletionTime == nil)
		},
	}
}

// drainReschedulingPredicate fires for drain Reschedulings created by this
// controller. Terminal states are included on purpose: the controller reacts to
// Succeeded (re-verify) and Failed (back off).
func drainReschedulingPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		rr, ok := obj.(*v1alpha1.Rescheduling)
		return ok && isDrain(rr)
	})
}

// ---- small helpers over Rescheduling / VirtualMachine ----

// isDrain reports whether a Rescheduling is a failover host drain created by this
// controller.
func isDrain(rr *v1alpha1.Rescheduling) bool {
	return rr.Spec.Action == v1alpha1.ReschedulingActionDrain &&
		rr.Labels[LabelFailoverHostDrain] == "true"
}

// drainState returns the effective state of a Rescheduling, treating the empty
// (unset) state as Pending to match the CRD default.
func drainState(rr *v1alpha1.Rescheduling) v1alpha1.ReschedulingState {
	if rr.Status.State == "" {
		return v1alpha1.ReschedulingStatePending
	}
	return rr.Status.State
}

// drainInFlight reports whether a drain in the given state is still being worked
// on (not terminal).
func drainInFlight(state v1alpha1.ReschedulingState) bool {
	switch state {
	case v1alpha1.ReschedulingStateSucceeded,
		v1alpha1.ReschedulingStateRejected,
		v1alpha1.ReschedulingStateFailed:
		return false
	default:
		return true
	}
}

// drainOccupiesSlot reports whether a drain in the given state occupies the
// single-drain-at-a-time slot. Failed still occupies it (it may hold partial
// state) until the rescheduler prunes it; Succeeded and Rejected do not.
func drainOccupiesSlot(state v1alpha1.ReschedulingState) bool {
	switch state {
	case v1alpha1.ReschedulingStateSucceeded, v1alpha1.ReschedulingStateRejected:
		return false
	default:
		return true
	}
}

// drainHosts returns the host names a drain Rescheduling targets.
func drainHosts(rr *v1alpha1.Rescheduling) []string {
	if rr.Spec.Drain == nil {
		return nil
	}
	hosts := make([]string, 0, len(rr.Spec.Drain.Target.Hosts))
	for _, h := range rr.Spec.Drain.Target.Hosts {
		if h.Host.Name != "" {
			hosts = append(hosts, h.Host.Name)
		}
	}
	return hosts
}

// drainTargetsHost reports whether a drain Rescheduling targets the given host.
func drainTargetsHost(rr *v1alpha1.Rescheduling, host string) bool {
	for _, h := range drainHosts(rr) {
		if h == host {
			return true
		}
	}
	return false
}

// reschedulingHostsIndexFn is the index function keying drain Reschedulings by the
// hosts they target.
func reschedulingHostsIndexFn(obj client.Object) []string {
	rr, ok := obj.(*v1alpha1.Rescheduling)
	if !ok || !isDrain(rr) {
		return nil
	}
	return drainHosts(rr)
}

// vmHost returns the Nova-observed host of a VirtualMachine, or "" if unplaced.
func vmHost(obj client.Object) string {
	vm, ok := obj.(*vmv1alpha1.VirtualMachine)
	if !ok || vm.Status.Nova == nil {
		return ""
	}
	return vm.Status.Nova.Host
}

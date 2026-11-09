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
	// controller, so they can be listed and counted by label rather than by name.
	LabelFailoverHostDrain = "reservations.cortex.cloud/failover-host-drain"

	// novaStatusDeleted is the Nova instance status reported for a server that has
	// been deleted. A virtual machine in this state no longer occupies its host.
	novaStatusDeleted = "DELETED"
)

// DrainControllerConfig is the top-level config section the drain controller reads
// from the shared cortex conf.json. Its settings live under failover.hosts.drain,
// alongside the partitioning controller's failover.hosts settings (same domain),
// but it is loaded as its own struct so the two controllers stay decoupled: the
// partitioning controller's ControllerConfig carries no drain-specific fields.
type DrainControllerConfig struct {
	Failover struct {
		Hosts struct {
			Drain DrainConfig `json:"drain"`
		} `json:"hosts"`
	} `json:"failover"`
}

// DrainConfig configures the failover host drain controller, which brings freshly
// designated failover host reservations into an Active state by ensuring the
// reserved host is empty of virtual machines. It is gated by Enabled, which is
// false by default: draining live-migrates workloads around the datacenter, so it
// is opt-in.
type DrainConfig struct {
	// Enabled gates the entire drain controller. When false (the default) the
	// controller is not started: no reservation is marked Active and no draining
	// happens.
	Enabled bool `json:"enabled,omitempty"`

	// TTL is the maximum time a reservation may stay non-Active (counted from its
	// creation) before the drain controller deletes it, so the partitioning
	// generator can designate a fresh one. It is deliberately conservative to
	// avoid the controller causing drains all over the datacenter. Defaults to 24h.
	TTL metav1.Duration `json:"ttl,omitempty"`

	// RetryBackoff is the requeue delay used while waiting for the rescheduler to
	// prune a Failed/Rejected drain Rescheduling before a fresh attempt. Defaults
	// to 1m.
	RetryBackoff metav1.Duration `json:"retryBackoff,omitempty"`
}

// ApplyDefaults fills in the default TTL and retry backoff when unset. Enabled is
// intentionally left at its zero value (false) so the controller stays opt-in.
func (d *DrainConfig) ApplyDefaults() {
	if d.TTL.Duration <= 0 {
		d.TTL = metav1.Duration{Duration: 24 * time.Hour}
	}
	if d.RetryBackoff.Duration <= 0 {
		d.RetryBackoff = metav1.Duration{Duration: time.Minute}
	}
}

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

	// 5. Host not empty: drive the drain. Find this host's drain and, in the same
	// scan, whether another host is already being drained (only one runs at a time).
	drains := &v1alpha1.ReschedulingList{}
	if err := c.List(ctx, drains, client.MatchingLabels{LabelFailoverHostDrain: "true"}); err != nil {
		return ctrl.Result{}, err
	}
	var forHost *v1alpha1.Rescheduling // the drain targeting this host, if any.
	otherInFlight := false             // a drain for a different host still occupying the single slot.
	for i := range drains.Items {
		rr := &drains.Items[i]
		if !isDrain(rr) {
			continue
		}
		if drainHostsContain(rr, host) {
			// Prefer a non-terminal drain for this host; otherwise keep the first
			// terminal one so we can act on its Succeeded/Failed state below.
			if forHost == nil || (isTerminalState(forHost.Status.State) && !isTerminalState(rr.Status.State)) {
				forHost = rr
			}
			continue
		}
		// A drain for another host occupies the single slot unless it is fully
		// terminal and clean (Succeeded/Rejected). Failed still holds the slot until
		// the rescheduler prunes it, as it may carry partial state.
		if s := rr.Status.State; s != v1alpha1.ReschedulingStateSucceeded && s != v1alpha1.ReschedulingStateRejected {
			otherInFlight = true
		}
	}

	// No drain for this host yet: create one unless another drain is in flight.
	if forHost == nil {
		if otherInFlight {
			log.V(1).Info("another drain is in flight, waiting before draining this host")
			return ctrl.Result{}, nil
		}
		log.Info("host not empty, creating drain rescheduling", "host", host)
		return ctrl.Result{}, c.createDrain(ctx, res, host)
	}

	switch forHost.Status.State {
	case v1alpha1.ReschedulingStateSucceeded:
		// The rescheduler reports the drain done. Do not trust it blindly (it may
		// be a stub, or a best-effort drain): the host emptiness was already checked
		// above and came back non-empty, so the drain left workload behind. The
		// Rescheduling is terminal and nothing re-drives it, so do not requeue: the
		// VM watch re-triggers us when workloads actually leave, and the TTL deletes
		// the reservation otherwise.
		log.Info("drain reported success but host still has virtual machines, leaving reservation designated")
		return ctrl.Result{}, nil
	case v1alpha1.ReschedulingStateFailed, v1alpha1.ReschedulingStateRejected:
		// The rescheduler owns cleaning up a failed request. Wait for it to prune
		// the object, then a fresh drain is created on a later reconcile.
		log.V(1).Info("drain rescheduling in terminal error state, backing off", "state", forHost.Status.State)
		return ctrl.Result{RequeueAfter: c.Config.RetryBackoff.Duration}, nil
	default:
		// Pending/Acknowledged/Processing/Accepted: in flight, wait for the watch.
		log.V(1).Info("drain rescheduling in flight, waiting", "state", forHost.Status.State)
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
			GenerateName: "drain-failover-host-" + host + "-",
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

	isType := func(want func(client.Object) bool) predicate.Predicate {
		return predicate.NewPredicateFuncs(want)
	}
	bldr := multicluster.BuildController(mcl, mgr)
	var err error
	// Reservation: enqueue the (cluster-scoped) reservation itself.
	if bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		c.mapToRequests(func(_ context.Context, obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
				return nil
			}
			return []string{res.Name}
		}),
		isType(func(obj client.Object) bool {
			res, ok := obj.(*v1alpha1.Reservation)
			return ok && res.Spec.Type == v1alpha1.ReservationTypeFailoverHost
		}),
	); err != nil {
		return err
	}
	// Hypervisor: enqueue the failover host reservations on that host.
	if bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		c.mapToRequests(c.reservationsOnHost(func(obj client.Object) string { return obj.GetName() })),
		isType(func(obj client.Object) bool { _, ok := obj.(*hv1.Hypervisor); return ok }),
	); err != nil {
		return err
	}
	// VirtualMachine: enqueue the failover host reservation of the VM's host.
	if bldr, err = bldr.WatchesMulticluster(&vmv1alpha1.VirtualMachine{},
		c.mapToRequests(c.reservationsOnHost(vmHost)), vmHostPredicate(),
	); err != nil {
		return err
	}
	// Rescheduling: enqueue the failover host reservation(s) the drain targets.
	if bldr, err = bldr.WatchesMulticluster(&v1alpha1.Rescheduling{},
		c.mapToRequests(func(_ context.Context, obj client.Object) []string {
			rr, ok := obj.(*v1alpha1.Rescheduling)
			if !ok {
				return nil
			}
			names := make([]string, 0, len(drainHosts(rr)))
			for _, host := range drainHosts(rr) {
				names = append(names, "failover-host-"+host)
			}
			return names
		}),
		isType(func(obj client.Object) bool { rr, ok := obj.(*v1alpha1.Rescheduling); return ok && isDrain(rr) }),
	); err != nil {
		return err
	}
	return bldr.Named("failoverhost-drain-controller").
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

// mapToRequests turns a function that maps a changed object to reservation names
// into an event handler that enqueues a reconcile for each on create/update/delete.
func (c *DrainController) mapToRequests(names func(context.Context, client.Object) []string) handler.EventHandler {
	enqueue := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		for _, name := range names(ctx, obj) {
			q.Add(ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.Object, q)
		},
	}
}

// reservationsOnHost returns a name-mapper that, given an object carrying a host
// (via hostOf), lists the failover host reservations on that host. Used for the
// Hypervisor and VirtualMachine watches via the shared reservation-by-host index.
func (c *DrainController) reservationsOnHost(hostOf func(client.Object) string) func(context.Context, client.Object) []string {
	return func(ctx context.Context, obj client.Object) []string {
		host := hostOf(obj)
		if host == "" {
			return nil
		}
		list := &v1alpha1.ReservationList{}
		if err := c.List(ctx, list, client.MatchingFields{reservations.IdxReservationByHost: host}); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list reservations for host", "host", host)
			return nil
		}
		names := make([]string, 0, len(list.Items))
		for i := range list.Items {
			if list.Items[i].Spec.Type == v1alpha1.ReservationTypeFailoverHost {
				names = append(names, list.Items[i].Name)
			}
		}
		return names
	}
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

// isDrain reports whether a Rescheduling is a failover host drain created by this
// controller.
func isDrain(rr *v1alpha1.Rescheduling) bool {
	return rr.Spec.Action == v1alpha1.ReschedulingActionDrain &&
		rr.Labels[LabelFailoverHostDrain] == "true"
}

// isTerminalState reports whether a drain in the given state is done being worked
// on (Succeeded, Rejected or Failed). An empty state is the Pending default, i.e.
// not terminal.
func isTerminalState(state v1alpha1.ReschedulingState) bool {
	switch state {
	case v1alpha1.ReschedulingStateSucceeded,
		v1alpha1.ReschedulingStateRejected,
		v1alpha1.ReschedulingStateFailed:
		return true
	default:
		return false
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

// drainHostsContain reports whether a drain Rescheduling targets the given host.
func drainHostsContain(rr *v1alpha1.Rescheduling, host string) bool {
	for _, h := range drainHosts(rr) {
		if h == host {
			return true
		}
	}
	return false
}

// vmHost returns the Nova-observed host of a VirtualMachine, or "" if unplaced.
func vmHost(obj client.Object) string {
	vm, ok := obj.(*vmv1alpha1.VirtualMachine)
	if !ok || vm.Status.Nova == nil {
		return ""
	}
	return vm.Status.Nova.Host
}

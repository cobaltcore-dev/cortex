// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// controller, so they can be listed by label.
	LabelFailoverHostDrain = "reservations.cortex.cloud/failover-host-drain"

	// ReasonHostDrained is the FailoverHostActive condition reason set once the
	// reserved host is empty.
	ReasonHostDrained = "HostDrained"

	// novaStatusDeleted is the Nova status of a deleted server, which no longer
	// occupies its host.
	novaStatusDeleted = "DELETED"
)

// DrainControllerConfig is the slice of the shared cortex config the drain
// controller reads. Its settings live under failover.hosts.drain next to the
// partitioning controller's, but are loaded separately to keep both decoupled.
type DrainControllerConfig struct {
	Failover struct {
		Hosts struct {
			Drain DrainConfig `json:"drain"`
		} `json:"hosts"`
	} `json:"failover"`
}

// DrainConfig configures the drain controller.
type DrainConfig struct {
	// Enabled gates the whole controller. Draining live-migrates workloads, so it
	// is opt-in: when false the controller is not started at all.
	Enabled bool `json:"enabled,omitempty"`
	// TTL is how long a reservation may stay non-Active (since creation) before
	// it is deleted so the generator can designate a fresh one. Defaults to 24h.
	TTL metav1.Duration `json:"ttl,omitempty"`
	// RetryBackoff is the requeue delay while waiting for the rescheduler to
	// prune a Failed/Rejected drain. Defaults to 1m.
	RetryBackoff metav1.Duration `json:"retryBackoff,omitempty"`
}

// ApplyDefaults fills in TTL and RetryBackoff when unset. Enabled stays false
// unless configured, keeping the controller opt-in.
func (d *DrainConfig) ApplyDefaults() {
	if d.TTL.Duration <= 0 {
		d.TTL = metav1.Duration{Duration: 24 * time.Hour}
	}
	if d.RetryBackoff.Duration <= 0 {
		d.RetryBackoff = metav1.Duration{Duration: time.Minute}
	}
}

// DrainController advances failover host reservations Designated -> Active by
// making sure the reserved host runs no virtual machines, draining it via a
// Rescheduling if needed. At most one such drain runs at a time fleet-wide.
//
// Ownership: the partitioning Controller writes reservation spec and labels;
// this controller owns the FailoverHostActive condition and deletes reservations
// that can no longer become Active. The Matcher only touches Active reservations.
type DrainController struct {
	client.Client
	Config DrainConfig
	// Monitor records metrics; nil disables them.
	Monitor *Monitor
}

// Reconcile drives one failover host reservation toward Active. It never
// demotes an Active reservation: once Active it may legitimately receive VMs
// from a gang evacuation (InUse), and flipping it back would break that.
func (c *DrainController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	res := &v1alpha1.Reservation{}
	if err := c.Get(ctx, req.NamespacedName, res); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !isFailoverHostReservation(res) || res.Spec.FailoverHostReservation == nil || res.Spec.TargetHost == "" {
		return ctrl.Result{}, nil
	}
	host := res.Spec.TargetHost
	log := logf.FromContext(ctx).WithName("failoverhost-drain").WithValues("reservation", res.Name, "host", host)
	ctx = logf.IntoContext(ctx, log)

	// A missing hypervisor can never be drained into a failover target. An
	// unready one is deliberately not a trigger: the generator designates hosts
	// regardless of readiness, so deleting here would fight it every interval.
	// Stuck reservations are cleaned up by the TTL instead.
	if err := c.Get(ctx, client.ObjectKey{Name: host}, &hv1.Hypervisor{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("get hypervisor %s: %w", host, err)
		}
		log.Info("hypervisor gone, deleting failover host reservation")
		return ctrl.Result{}, c.deleteReservation(ctx, res)
	}

	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive) {
		return ctrl.Result{}, nil
	}

	ttlLeft := c.Config.TTL.Duration - time.Since(res.CreationTimestamp.Time)
	if ttlLeft <= 0 {
		log.Info("failover host reservation exceeded drain TTL, deleting", "ttl", c.Config.TTL.Duration)
		return ctrl.Result{}, c.deleteReservation(ctx, res)
	}

	empty, err := c.hostEmpty(ctx, host)
	if err != nil {
		return ctrl.Result{}, err
	}
	if empty {
		log.Info("host is empty, marking failover host reservation active")
		return ctrl.Result{}, c.markActive(ctx, res)
	}
	return c.driveDrain(ctx, res, ttlLeft)
}

// driveDrain moves an occupied host's drain forward: it creates a drain when
// none exists and the fleet-wide slot is free, and otherwise waits on the
// existing one. Waiting results requeue at the TTL deadline so the TTL fires
// even if no watch event arrives.
func (c *DrainController) driveDrain(ctx context.Context, res *v1alpha1.Reservation, ttlLeft time.Duration) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	host := res.Spec.TargetHost
	waitUntilTTL := ctrl.Result{RequeueAfter: ttlLeft}

	drains := &v1alpha1.ReschedulingList{}
	if err := c.List(ctx, drains, client.MatchingLabels{LabelFailoverHostDrain: "true"}); err != nil {
		return ctrl.Result{}, fmt.Errorf("list drain reschedulings: %w", err)
	}
	var own *v1alpha1.Rescheduling
	slotTaken := false
	for i := range drains.Items {
		rr := &drains.Items[i]
		switch {
		case rr.Spec.Action != v1alpha1.ReschedulingActionDrain:
			continue
		case drainTargetsHost(rr, host):
			// Several drains may exist for a host (Succeeded ones are never pruned);
			// prefer the one still in progress.
			if own == nil || (isTerminalState(own.Status.State) && !isTerminalState(rr.Status.State)) {
				own = rr
			}
		case holdsDrainSlot(rr.Status.State):
			slotTaken = true
		}
	}

	if own == nil {
		if slotTaken {
			log.V(1).Info("another failover host drain is in flight, waiting")
			return waitUntilTTL, nil
		}
		log.Info("host not empty, creating drain rescheduling")
		return ctrl.Result{}, c.createDrain(ctx, res)
	}

	switch own.Status.State {
	case v1alpha1.ReschedulingStateSucceeded:
		// The host was just found occupied, so the drain left workload behind. Do
		// not re-drain: the VM watch triggers us once workloads leave, and the TTL
		// deletes the reservation otherwise.
		log.Info("drain reported success but host still has virtual machines, leaving reservation designated")
		return waitUntilTTL, nil
	case v1alpha1.ReschedulingStateFailed, v1alpha1.ReschedulingStateRejected:
		// The rescheduler prunes failed drains; a fresh one is created afterwards.
		log.V(1).Info("drain rescheduling failed, backing off", "state", own.Status.State)
		return ctrl.Result{RequeueAfter: min(c.Config.RetryBackoff.Duration, ttlLeft)}, nil
	default:
		log.V(1).Info("drain rescheduling in flight, waiting", "state", own.Status.State)
		return waitUntilTTL, nil
	}
}

// hostEmpty reports whether no live virtual machine runs on the host. A VM
// counts only if Nova reports it there and it is neither pruned nor deleted.
func (c *DrainController) hostEmpty(ctx context.Context, host string) (bool, error) {
	vms := &vmv1alpha1.VirtualMachineList{}
	if err := c.List(ctx, vms); err != nil {
		return false, fmt.Errorf("list virtual machines: %w", err)
	}
	for _, vm := range vms.Items {
		if novaHost(&vm) != host || vm.Status.DeletionTime != nil || vm.Status.Nova.Status == novaStatusDeleted {
			continue
		}
		return false, nil
	}
	return true, nil
}

func (c *DrainController) markActive(ctx context.Context, res *v1alpha1.Reservation) error {
	base := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeFailoverHostActive,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: res.Generation,
		Reason:             ReasonHostDrained,
		Message:            "no virtual machines remain on the reserved host",
	})
	if err := c.Status().Patch(ctx, res, client.MergeFrom(base)); err != nil {
		c.Monitor.RecordReservationError("drain_mark_active")
		return fmt.Errorf("mark reservation %s active: %w", res.Name, err)
	}
	return nil
}

func (c *DrainController) deleteReservation(ctx context.Context, res *v1alpha1.Reservation) error {
	if err := client.IgnoreNotFound(c.Delete(ctx, res)); err != nil {
		c.Monitor.RecordReservationError("drain_delete")
		return fmt.Errorf("delete reservation %s: %w", res.Name, err)
	}
	c.Monitor.RecordReservationDeleted()
	return nil
}

// createDrain creates a drain Rescheduling for the reservation's host. The
// generated name avoids clashing with lingering Succeeded drains. Reserve is
// false because the failover host reservation already blocks the host.
func (c *DrainController) createDrain(ctx context.Context, res *v1alpha1.Reservation) error {
	host, az := res.Spec.TargetHost, res.Spec.AvailabilityZone
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
			AvailabilityZone: az,
			Drain: &v1alpha1.DrainActionSpec{
				Reserve: false,
				Target: v1alpha1.DrainTarget{Hosts: []v1alpha1.DrainTargetHost{{
					Host: v1alpha1.HostRef{Name: host, AvailabilityZone: az},
				}}},
				Constraints: v1alpha1.DrainConstraints{Mode: v1alpha1.DrainConstraintModeAll},
			},
		},
	}
	if err := c.Create(ctx, rr); err != nil {
		c.Monitor.RecordReservationError("drain_create")
		return fmt.Errorf("create drain rescheduling for host %s: %w", host, err)
	}
	return nil
}

// SetupWithManager registers the controller. Besides failover host
// reservations it watches Hypervisors (host removal), VirtualMachines (host
// emptiness) and its own drain Reschedulings (drain progress and the
// fleet-wide drain slot freeing up). Reconciles run one at a time, which keeps
// the single-drain bookkeeping race-free.
func (c *DrainController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	if err := reservations.IndexReservationByHost(ctx, mcl); err != nil {
		return fmt.Errorf("index reservations by host: %w", err)
	}

	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			res, ok := obj.(*v1alpha1.Reservation)
			return ok && isFailoverHostReservation(res)
		}),
	)
	if err != nil {
		return fmt.Errorf("watch reservations: %w", err)
	}
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			return c.failoverReservationsOnHost(ctx, obj.GetName())
		}),
	)
	if err != nil {
		return fmt.Errorf("watch hypervisors: %w", err)
	}
	bldr, err = bldr.WatchesMulticluster(&vmv1alpha1.VirtualMachine{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			vm, ok := obj.(*vmv1alpha1.VirtualMachine)
			if !ok {
				return nil
			}
			return c.failoverReservationsOnHost(ctx, novaHost(vm))
		}),
		vmEmptinessChangedPredicate(),
	)
	if err != nil {
		return fmt.Errorf("watch virtual machines: %w", err)
	}
	// Any drain change may free the fleet-wide slot, so wake every designated
	// reservation rather than only the drained host's.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.Rescheduling{},
		handler.EnqueueRequestsFromMapFunc(c.designatedFailoverReservations),
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[LabelFailoverHostDrain] == "true"
		}),
	)
	if err != nil {
		return fmt.Errorf("watch reschedulings: %w", err)
	}
	return bldr.Named("failoverhost-drain-controller").
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

// failoverReservationsOnHost maps a host to its failover host reservations via
// the shared reservation-by-host index.
func (c *DrainController) failoverReservationsOnHost(ctx context.Context, host string) []reconcile.Request {
	if host == "" {
		return nil
	}
	list := &v1alpha1.ReservationList{}
	if err := c.List(ctx, list, client.MatchingFields{reservations.IdxReservationByHost: host}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list reservations for host", "host", host)
		return nil
	}
	return requestsFor(list.Items, isFailoverHostReservation)
}

// designatedFailoverReservations maps any event to all failover host
// reservations that are not yet Active.
func (c *DrainController) designatedFailoverReservations(ctx context.Context, _ client.Object) []reconcile.Request {
	list := &v1alpha1.ReservationList{}
	if err := c.List(ctx, list, client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list failover host reservations")
		return nil
	}
	return requestsFor(list.Items, func(res *v1alpha1.Reservation) bool {
		return isFailoverHostReservation(res) &&
			!meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostActive)
	})
}

func requestsFor(items []v1alpha1.Reservation, keep func(*v1alpha1.Reservation) bool) []reconcile.Request {
	var reqs []reconcile.Request
	for i := range items {
		if keep(&items[i]) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&items[i])})
		}
	}
	return reqs
}

// vmEmptinessChangedPredicate passes VM updates only when the VM's host or
// deletion state changed, the only transitions that affect host emptiness.
func vmEmptinessChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldVM, okOld := e.ObjectOld.(*vmv1alpha1.VirtualMachine)
			newVM, okNew := e.ObjectNew.(*vmv1alpha1.VirtualMachine)
			if !okOld || !okNew {
				return false
			}
			return novaHost(oldVM) != novaHost(newVM) ||
				(oldVM.Status.DeletionTime == nil) != (newVM.Status.DeletionTime == nil)
		},
	}
}

func isFailoverHostReservation(res *v1alpha1.Reservation) bool {
	return res.Spec.Type == v1alpha1.ReservationTypeFailoverHost
}

// novaHost returns the host Nova reports the VM on, or "" if not placed yet.
func novaHost(vm *vmv1alpha1.VirtualMachine) string {
	if vm.Status.Nova == nil {
		return ""
	}
	return vm.Status.Nova.Host
}

func drainTargetsHost(rr *v1alpha1.Rescheduling, host string) bool {
	return rr.Spec.Drain != nil && slices.ContainsFunc(rr.Spec.Drain.Target.Hosts,
		func(h v1alpha1.DrainTargetHost) bool { return h.Host.Name == host })
}

// isTerminalState reports whether a drain is no longer being worked on. The
// empty state is the Pending default.
func isTerminalState(state v1alpha1.ReschedulingState) bool {
	switch state {
	case v1alpha1.ReschedulingStateSucceeded, v1alpha1.ReschedulingStateRejected, v1alpha1.ReschedulingStateFailed:
		return true
	default:
		return false
	}
}

// holdsDrainSlot reports whether a drain in this state still occupies the
// fleet-wide single-drain slot. Failed drains keep holding it until the
// rescheduler prunes them, as they may carry partial state.
func holdsDrainSlot(state v1alpha1.ReschedulingState) bool {
	return state != v1alpha1.ReschedulingStateSucceeded && state != v1alpha1.ReschedulingStateRejected
}

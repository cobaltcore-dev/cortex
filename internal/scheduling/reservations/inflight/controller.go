// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// Field index keys used by the controller and the owner handlers.
const (
	idxReservationByTargetHost  = "spec.targetHost"
	idxReservationByOperationID = "spec.inFlightReservation.operationID"
	idxReservationByVMID        = "spec.inFlightReservation.vmID"
)

// indexReservationByTargetHost indexes in-flight reservations by their target
// host so handlers can requeue reservations affected by a hypervisor change.
func indexReservationByTargetHost(obj client.Object) []string {
	res, ok := obj.(*v1alpha1.Reservation)
	if !ok || res.Spec.Type != v1alpha1.ReservationTypeInFlight || res.Spec.TargetHost == "" {
		return nil
	}
	return []string{res.Spec.TargetHost}
}

// indexReservationByOperationID indexes in-flight reservations by their
// operation id so the whole group can be released together.
func indexReservationByOperationID(obj client.Object) []string {
	res, ok := obj.(*v1alpha1.Reservation)
	if !ok || res.Spec.Type != v1alpha1.ReservationTypeInFlight {
		return nil
	}
	if res.Spec.InFlightReservation == nil || res.Spec.InFlightReservation.OperationID == "" {
		return nil
	}
	return []string{res.Spec.InFlightReservation.OperationID}
}

// indexReservationByVMID indexes in-flight reservations by their subject VM uuid
// so a handler can find all operations for one VM (e.g. for supersession).
func indexReservationByVMID(obj client.Object) []string {
	res, ok := obj.(*v1alpha1.Reservation)
	if !ok || res.Spec.Type != v1alpha1.ReservationTypeInFlight {
		return nil
	}
	if res.Spec.InFlightReservation == nil || res.Spec.InFlightReservation.VMID == "" {
		return nil
	}
	return []string{res.Spec.InFlightReservation.VMID}
}

// Controller owns the lifecycle of in-flight reservations. It reconciles each
// in-flight reservation as the primary resource and, based on the reservation's
// owner-reference Kind, dispatches to an OwnerHandler that decides when the
// owning operation is complete. On completion all reservations sharing the same
// OperationID are released together; a TTL backstop releases reservations whose
// owning operation never signals completion.
type Controller struct {
	client.Client

	// Monitor records controller metrics (e.g. TTL expiries). May be nil.
	Monitor *Monitor

	// DefaultOwnerKind is the owner handler Kind applied to in-flight
	// reservations that carry no recognised owner reference (fallback during
	// the transition before producers set owner references). Only applied to
	// Nova reservations that carry a VM subject. Typically "VirtualMachine".
	DefaultOwnerKind string

	// handlers maps an owner-reference Kind to its OwnerHandler. Built in
	// SetupWithManager from the registered handlers.
	handlers map[string]OwnerHandler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.2/pkg/reconcile
//
// For more details about the method shape, read up here:
// - https://ahmet.im/blog/controller-pitfalls/#reconcile-method-shape
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Reconciling resource")

	obj := new(v1alpha1.Reservation)
	if err := c.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it
			// was deleted (e.g. by a sibling reservation's batch delete) or not
			// created.
			log.V(1).Info("Resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get resource")
		return ctrl.Result{}, err
	}

	// Sanity checks which should always succeed due to the predicate. If a
	// non-in-flight reservation slips through we must not touch it: its status
	// belongs to another controller (committed/failover), so we only log.
	if obj.Spec.Type != v1alpha1.ReservationTypeInFlight {
		log.Error(errors.New("unexpected reservation type"),
			"Received a reservation with an unexpected type, ignoring",
			"reservationType", obj.Spec.Type)
		return ctrl.Result{}, nil
	}
	if obj.Spec.InFlightReservation == nil {
		log.Error(errors.New("missing in-flight reservation spec"),
			"Received a reservation with missing in-flight reservation spec")
		return ctrl.Result{}, c.setReadyCondition(ctx, obj, metav1.ConditionFalse,
			"MissingSpec", "In-flight reservation spec is required when type is InFlightReservation")
	}

	// TTL backstop: if the owning operation never signalled completion and the
	// reservation outlived its EndTime, emit a metric and release it so an
	// abandoned operation cannot strand capacity forever.
	if expired, err := c.handleTTL(ctx, obj); err != nil || expired {
		return ctrl.Result{}, err
	}

	handler := c.handlerFor(ctx, obj)
	if handler == nil {
		// No handler for this owner kind: we cannot determine terminality, so we
		// surface it on the status, emit a metric to alert on, and rely on the
		// TTL backstop for cleanup.
		log.Info("No owner handler for reservation, awaiting TTL",
			"name", obj.Name, "ownerReferences", obj.OwnerReferences)
		c.Monitor.RecordAnomaly(AnomalyNoOwnerHandler)
		return ctrl.Result{}, c.setReadyCondition(ctx, obj, metav1.ConditionUnknown,
			"NoOwnerHandler", "No owner handler registered for this reservation's owner")
	}

	// Bind a per-subject reservation lookup scoped to the selected handler, so
	// the handler sees exactly the reservations reconciliation would route to
	// it (same ownership selection), without reimplementing that logic.
	lookup := func(ctx context.Context, vmID string) ([]v1alpha1.Reservation, error) {
		return c.reservationsForSubjectHandler(ctx, handler.Kind(), vmID)
	}
	result, err := handler.TerminalState(ctx, obj, lookup)
	if err != nil {
		// Surface transient errors so controller-runtime applies its backoff.
		log.Error(err, "Owner handler failed to determine terminal state")
		return ctrl.Result{}, err
	}
	if !result.Done {
		// Record why we are still waiting; a nil error keeps our RequeueAfter.
		reason, message := result.Reason, result.Message
		if reason == "" {
			reason, message = "AwaitingCompletion", "The owning operation has not reached a terminal state yet"
		}
		if err := c.setReadyCondition(ctx, obj, metav1.ConditionUnknown, reason, message); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: result.RequeueAfter}, nil
	}

	// The owning operation completed: release all reservations in the group.
	return ctrl.Result{}, c.releaseOperation(ctx, obj)
}

// handlerFor returns the OwnerHandler responsible for the reservation's owner
// kind, emitting the ambiguous-owner anomaly metric when more than one handler
// matches. It wraps resolveHandler, which carries the pure selection logic so it
// can be reused (e.g. by the per-subject lookup) without inflating metrics.
func (c *Controller) handlerFor(ctx context.Context, res *v1alpha1.Reservation) OwnerHandler {
	chosen, ambiguous := c.resolveHandler(res)
	if ambiguous {
		c.Monitor.RecordAnomaly(AnomalyAmbiguousOwner)
		ctrl.LoggerFrom(ctx).Info("Reservation has multiple owner references mapping to different handlers, using the first",
			"name", res.Name, "chosenKind", chosen.Kind())
	}
	return chosen
}

// resolveHandler selects the OwnerHandler for a reservation without side
// effects. Owner references are inspected in order; the first one mapping to a
// registered handler wins, and ambiguous is true when more than one distinct
// handler matched (a misconfigured ownership).
//
// The DefaultOwnerKind fallback applies only to a Nova VM-subject reservation
// that carries NO owner references at all (the transition period before
// producers set them). A reservation that does carry an owner reference but no
// registered handler matches is deliberately left without a handler (surfacing
// as NoOwnerHandler), so e.g. a Rescheduling-owned reservation is never handled
// by the VirtualMachine handler just because it also carries a vmID.
func (c *Controller) resolveHandler(res *v1alpha1.Reservation) (handler OwnerHandler, ambiguous bool) {
	var chosen OwnerHandler
	matched := map[string]bool{}
	for _, ref := range res.OwnerReferences {
		h, ok := c.handlers[ref.Kind]
		if !ok {
			continue
		}
		matched[ref.Kind] = true
		if chosen == nil {
			chosen = h
		}
	}
	if chosen != nil {
		return chosen, len(matched) > 1
	}
	// Fallback: a Nova VM-subject reservation with no owner references at all is
	// handled by the configured default owner handler. A reservation that has
	// owner references but none recognised is intentionally not handled here.
	if len(res.OwnerReferences) == 0 &&
		c.DefaultOwnerKind != "" &&
		res.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova &&
		res.Spec.InFlightReservation != nil && res.Spec.InFlightReservation.VMID != "" {
		return c.handlers[c.DefaultOwnerKind], false
	}
	return nil, false
}

// reservationsForSubjectHandler returns the in-flight reservations with the
// given VM subject that resolve to the given handler kind. It applies the same
// ownership selection as reconciliation (resolveHandler), so "owned by this
// handler" means the same thing when dispatching and when a handler inspects
// sibling reservations. This backs the SubjectReservations lookup passed to
// TerminalState and is read-only (no metrics).
func (c *Controller) reservationsForSubjectHandler(ctx context.Context, handlerKind, vmID string) ([]v1alpha1.Reservation, error) {
	list := &v1alpha1.ReservationList{}
	if err := c.List(ctx, list, client.MatchingFields{idxReservationByVMID: vmID}); err != nil {
		return nil, err
	}
	out := list.Items[:0]
	for i := range list.Items {
		res := &list.Items[i]
		if h, _ := c.resolveHandler(res); h != nil && h.Kind() == handlerKind {
			out = append(out, *res)
		}
	}
	return out, nil
}

// handleTTL releases the reservation if it has exceeded its EndTime. It returns
// expired=true when the reservation was released (or its release failed).
func (c *Controller) handleTTL(ctx context.Context, res *v1alpha1.Reservation) (bool, error) {
	if res.Spec.EndTime == nil {
		return false, nil
	}
	if time.Now().Before(res.Spec.EndTime.Time) {
		return false, nil
	}
	log := ctrl.LoggerFrom(ctx)
	log.Info("In-flight reservation exceeded its TTL, releasing",
		"name", res.Name,
		"endTime", res.Spec.EndTime.Time,
		"intent", res.Spec.InFlightReservation.Intent)
	c.Monitor.RecordTTLExpiry(res.Spec.InFlightReservation.Intent)
	// Release the whole operation: a TTL expiry means the operation is
	// abandoned, so its sibling reservations are stale too.
	return true, c.releaseOperation(ctx, res)
}

// releaseOperation deletes every in-flight reservation that shares the given
// reservation's OperationID (including the reservation itself). When no
// OperationID is set we fall back to deleting just this reservation, so legacy
// reservations are still cleaned up. Deletion is idempotent: siblings already
// removed by a concurrent reconcile are ignored.
func (c *Controller) releaseOperation(ctx context.Context, res *v1alpha1.Reservation) error {
	log := ctrl.LoggerFrom(ctx)
	operationID := res.Spec.InFlightReservation.OperationID

	if operationID == "" {
		log.V(1).Info("Releasing single in-flight reservation (no operation id)", "name", res.Name)
		return client.IgnoreNotFound(c.Delete(ctx, res))
	}

	siblings := new(v1alpha1.ReservationList)
	if err := c.List(ctx, siblings, client.MatchingFields{idxReservationByOperationID: operationID}); err != nil {
		log.Error(err, "Failed to list reservations for operation", "operationID", operationID)
		return err
	}
	for i := range siblings.Items {
		sibling := &siblings.Items[i]
		if err := client.IgnoreNotFound(c.Delete(ctx, sibling)); err != nil {
			log.Error(err, "Failed to delete reservation",
				"reservation", sibling.Name, "operationID", operationID)
			return err
		}
		log.V(1).Info("Released in-flight reservation",
			"reservation", sibling.Name, "operationID", operationID)
	}
	return nil
}

// setReadyCondition patches the reservation's Ready condition via the status
// subresource.
func (c *Controller) setReadyCondition(ctx context.Context, res *v1alpha1.Reservation, status metav1.ConditionStatus, reason, message string) error {
	orig := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ReservationConditionReady,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	if err := c.Status().Patch(ctx, res, client.MergeFrom(orig)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to update reservation status")
		return err
	}
	return nil
}

// predicateReservations generates a new predicate for in flight reservations.
func (c *Controller) predicateReservations() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		reservation, ok := object.(*v1alpha1.Reservation)
		if !ok {
			return false // Not a Reservation object.
		}
		if reservation.Spec.Type != v1alpha1.ReservationTypeInFlight {
			return false // Not an in-flight reservation.
		}
		if reservation.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
			return false // Not a Nova reservation.
		}
		return true // Reconcile.
	})
}

// hypervisorInstancesChangedPredicate filters Hypervisor events to only those
// that change Status.Instances, since that is the only field the VM owner
// handler consumes. Without it, unrelated status updates from the hypervisor
// operator would cause a list + enqueue of every reservation targeting the
// host.
func hypervisorInstancesChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(evt event.CreateEvent) bool {
			_, ok := evt.Object.(*hv1.Hypervisor)
			return ok
		},
		DeleteFunc: func(evt event.DeleteEvent) bool {
			_, ok := evt.Object.(*hv1.Hypervisor)
			return ok
		},
		GenericFunc: func(evt event.GenericEvent) bool {
			_, ok := evt.Object.(*hv1.Hypervisor)
			return ok
		},
		UpdateFunc: func(evt event.UpdateEvent) bool {
			oldHV, ok := evt.ObjectOld.(*hv1.Hypervisor)
			if !ok {
				return false
			}
			newHV, ok := evt.ObjectNew.(*hv1.Hypervisor)
			if !ok {
				return false
			}
			return !reflect.DeepEqual(oldHV.Status.Instances, newHV.Status.Instances)
		},
	}
}

// SetupWithManager sets up the controller with the Manager and a multicluster
// client. The multicluster client is used to watch for changes in the
// Reservation CRD across all clusters and trigger reconciliations accordingly.
// The watch set is the union of the primary Reservation watch and every
// registered owner handler's declared secondary watches (deduplicated by type).
func (c *Controller) SetupWithManager(ctx context.Context, mgr ctrl.Manager) (err error) {
	// Check that the provided client is a multicluster client, since we need
	// that to watch across clusters. Do this before adding any runnables so a
	// misconfigured setup fails fast.
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}

	// Register the built-in owner handlers.
	vmHandler := &VirtualMachineHandler{Client: c.Client}
	c.handlers = map[string]OwnerHandler{vmHandler.Kind(): vmHandler}

	// Index reservations by their target host, operation id and VM uuid so
	// handlers can requeue by host, release a whole operation group, and find
	// all operations for one VM (supersession).
	if err := mcl.IndexField(ctx, &v1alpha1.Reservation{}, &v1alpha1.ReservationList{},
		idxReservationByTargetHost, indexReservationByTargetHost); err != nil {
		return err
	}
	if err := mcl.IndexField(ctx, &v1alpha1.Reservation{}, &v1alpha1.ReservationList{},
		idxReservationByOperationID, indexReservationByOperationID); err != nil {
		return err
	}
	if err := mcl.IndexField(ctx, &v1alpha1.Reservation{}, &v1alpha1.ReservationList{},
		idxReservationByVMID, indexReservationByVMID); err != nil {
		return err
	}

	bldr := multicluster.BuildController(mcl, mgr)
	// Primary resource: the in-flight reservation.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		&handler.EnqueueRequestForObject{},
		c.predicateReservations(),
	)
	if err != nil {
		return err
	}

	// Secondary resources: one watch per (handler, declared object). We do not
	// deduplicate across handlers watching the same type — controller-runtime
	// shares the informer/cache per GVK, each handler keeps its own MapFn and
	// predicate, and the workqueue coalesces any duplicate enqueues. Each
	// handler's MapFn only enqueues reservations it owns, and Reconcile
	// re-dispatches by owner kind, so cross-handler events are harmless.
	for _, h := range c.handlers {
		for _, w := range h.WatchObjects() {
			preds := []predicate.Predicate{}
			if w.Predicate != nil {
				preds = append(preds, w.Predicate)
			}
			bldr, err = bldr.WatchesMulticluster(w.Object,
				handler.EnqueueRequestsFromMapFunc(w.MapFn), preds...)
			if err != nil {
				return err
			}
		}
	}

	return bldr.Named("inflight-reservation-controller").
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(c)
}

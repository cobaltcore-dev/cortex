// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"errors"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	idxReservationByTargetHost   = "spec.targetHost"
	idxReservationByTargetHostFn = func(obj client.Object) []string {
		res, ok := obj.(*v1alpha1.Reservation)
		if !ok {
			return nil
		}
		if res.Spec.TargetHost == "" {
			return nil
		}
		return []string{res.Spec.TargetHost}
	}
)

// Controller owns the lifecycle of in-flight reservations.
type Controller struct{ client.Client }

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
			// If the custom resource is not found then it usually means
			// that it was deleted or not created.
			log.Info("Resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get resource")
		return ctrl.Result{}, err
	}

	// Sanity checks which should always succeed due to the predicate.
	if obj.Spec.Type != v1alpha1.ReservationTypeInFlight {
		log.Error(errors.New("unexpected reservation type"),
			"Received a reservation with an unexpected type",
			"reservationType", obj.Spec.Type)
		orig := obj.DeepCopy()
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UnexpectedType",
			Message: "Expected reservation type to be InFlightReservation",
		})
		return ctrl.Result{}, c.Status().Patch(ctx, obj, client.MergeFrom(orig))
	}
	if obj.Spec.InFlightReservation == nil {
		log.Error(errors.New("missing in-flight reservation spec"),
			"Received a reservation with missing in-flight reservation spec")
		orig := obj.DeepCopy()
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingSpec",
			Message: "In-flight reservation spec is required when type is InFlightReservation",
		})
		return ctrl.Result{}, c.Status().Patch(ctx, obj, client.MergeFrom(orig))
	}

	// Get a list of all hypervisors and check if the instance
	// has spawned on any of them.
	hvs := new(hv1.HypervisorList)
	if err := c.List(ctx, hvs); err != nil {
		log.Error(err, "Failed to list hypervisors")
		return ctrl.Result{}, err
	}
	found := false
	hypervisorName := ""
	for _, hv := range hvs.Items {
		for _, instance := range hv.Status.Instances {
			if instance.ID == obj.Spec.InFlightReservation.VMID {
				found = true
				hypervisorName = hv.Name
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		// The instance has not spawned on any hypervisor (yet).
		// Requeue and check again later.
		log.V(1).Info("Instance has not spawned on any hypervisor yet, requeuing",
			"vmID", obj.Spec.InFlightReservation.VMID)
		// TODO: monitor this & alert
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionUnknown,
			Reason:  "InstanceNotFound",
			Message: "The instance has not spawned on any hypervisor yet",
		})
		if err := c.Status().Update(ctx, obj); err != nil {
			log.Error(err, "Failed to update reservation status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// We don't care where the instance came up. Even if this in-flight
	// reservation is for another host, we can prune it.
	log.Info("Instance has spawned on a hypervisor, removing in-flight reservation",
		"vmID", obj.Spec.InFlightReservation.VMID,
		"hypervisor", hypervisorName)
	return ctrl.Result{}, c.Delete(ctx, obj)
}

// handleReservations generates a new event handler for in flight reservations.
func (c *Controller) handleReservations() handler.EventHandler {
	handler := handler.Funcs{}
	handler.CreateFunc = func(ctx context.Context, evt event.CreateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.Object.(*v1alpha1.Reservation).Name, // cluster-scoped crd
		}})
	}
	handler.UpdateFunc = func(ctx context.Context, evt event.UpdateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.ObjectOld.(*v1alpha1.Reservation).Name, // cluster-scoped crd
		}})
	}
	handler.DeleteFunc = func(ctx context.Context, evt event.DeleteEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.Object.(*v1alpha1.Reservation).Name, // cluster-scoped crd
		}})
	}
	return handler
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
		return true // Reconcile.
	})
}

// handleHypervisors generates a new event handler for hypervisors.
func (c *Controller) handleHypervisors() handler.EventHandler {
	handler := handler.Funcs{}
	enqueueCorrespondingReservations := func(ctx context.Context, hvName string,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		log := ctrl.LoggerFrom(ctx)
		log.V(1).Info("Enqueuing reservations corresponding to hypervisor",
			"hypervisor", hvName)
		// Requeue all reservations targeting this hypervisor, since the
		// instance list has changed and we might find the instance for
		// some in-flight reservation now.
		reservations := &v1alpha1.ReservationList{}
		if err := c.List(ctx, reservations, client.MatchingFields{
			idxReservationByTargetHost: hvName,
		}); err != nil {
			log.Error(err, "Failed to list reservations for hypervisor",
				"hypervisor", hvName)
			return
		}
		for _, res := range reservations.Items {
			log.V(1).Info("Enqueuing reservation for reconciliation",
				"reservation", res.Name,
				"targetHost", res.Spec.TargetHost,
				"hypervisor", hvName)
			queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
				Name: res.Name, // cluster-scoped crd
			}})
		}
	}
	handler.CreateFunc = func(ctx context.Context, evt event.CreateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		hv := evt.Object.(*hv1.Hypervisor)
		enqueueCorrespondingReservations(ctx, hv.Name, queue)
	}
	handler.UpdateFunc = func(ctx context.Context, evt event.UpdateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		hv := evt.ObjectNew.(*hv1.Hypervisor)
		enqueueCorrespondingReservations(ctx, hv.Name, queue)
	}
	handler.DeleteFunc = func(ctx context.Context, evt event.DeleteEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		hv := evt.Object.(*hv1.Hypervisor)
		enqueueCorrespondingReservations(ctx, hv.Name, queue)
	}
	return handler
}

// predicateHypervisors generates a new predicate for hypervisors.
func (c *Controller) predicateHypervisors() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		_, ok := object.(*hv1.Hypervisor)
		return ok
	})
}

// SetupWithManager sets up the controller with the Manager and a multicluster
// client. The multicluster client is used to watch for changes in the
// Reservation CRD across all clusters and trigger reconciliations accordingly.
func (c *Controller) SetupWithManager(ctx context.Context, mgr ctrl.Manager) (err error) {
	// Check that the provided client is a multicluster client, since we need
	// that to watch for hypervisors across clusters.
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	bldr := multicluster.BuildController(mcl, mgr)
	// The reservation crd & hypervisor crd may be distributed across multiple
	// remote clusters.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		c.handleReservations(),
		c.predicateReservations(),
	)
	// Index reservations by their target host so we can requeue reservations
	// for which the list of instances on a hypervisor has changed.
	mcl.IndexField(ctx,
		&v1alpha1.Reservation{},
		&v1alpha1.ReservationList{},
		idxReservationByTargetHost,
		idxReservationByTargetHostFn,
	)
	// Watch hypervisor changes and requeue reservations targeting
	// the changed hypervisor.
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		c.handleHypervisors(),
		c.predicateHypervisors(),
	)
	if err != nil {
		return err
	}
	return bldr.Named("inflight-reservation-controller").
		Complete(c)
}

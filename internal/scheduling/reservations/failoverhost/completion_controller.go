// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// CompletionControllerConfig is the slice of the shared cortex config the
// completion controller reads. Its settings live under failoverhost.completion
// next to failoverhost.{partitioning,drain}, but are loaded separately to keep
// the controllers decoupled.
type CompletionControllerConfig struct {
	FailoverHost struct {
		Completion CompletionConfig `json:"completion"`
	} `json:"failoverhost"`
}

// CompletionConfig configures the completion controller.
type CompletionConfig struct {
	// Enabled gates the whole controller. Completing a reservation deletes it
	// (turning the reserved host back into a regular host), so it is opt-in:
	// when false the controller is not started at all.
	Enabled bool `json:"enabled,omitempty"`
	// GracePeriod is how long the source host must stay evicted (measured from the
	// FailoverHostCompleted condition's lastTransitionTime) before the InUse
	// reservation is deleted. It guards against rapid evacuate<->reserve loops
	// when virtual machines flap. Defaults to 24h.
	GracePeriod metav1.Duration `json:"gracePeriod,omitempty"`
}

// ApplyDefaults fills in GracePeriod when unset. Enabled stays false unless
// configured, keeping the controller opt-in.
func (c *CompletionConfig) ApplyDefaults() {
	if c.GracePeriod.Duration <= 0 {
		c.GracePeriod = metav1.Duration{Duration: 24 * time.Hour}
	}
}

// CompletionController finishes the failover host lifecycle. It watches InUse
// failover host reservations and, once the evacuated source host reports itself
// evicted (no instances left), waits out a grace period before deleting the
// reservation. The deletion turns the reserved host back into a regular host,
// which keeps hosting the failed-over workload.
//
// Ownership: the Matcher writes the FailoverHostInUse condition and SourceHost;
// the drain controller owns FailoverHostActive and deliberately leaves Active and
// InUse reservations alone. This controller owns FailoverHostCompleted and the
// final deletion of InUse reservations.
type CompletionController struct {
	client.Client
	Config CompletionConfig
	// Monitor records metrics; nil disables them.
	Monitor *Monitor
}

// Reconcile drives one InUse failover host reservation toward completion. It acts
// only on reservations the Matcher has marked InUse: once the source host they
// rescued reports itself evicted it marks them FailoverHostCompleted and, after
// the grace period, deletes them.
func (c *CompletionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	res := &v1alpha1.Reservation{}
	if err := c.Get(ctx, req.NamespacedName, res); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if res.Spec.Type != v1alpha1.ReservationTypeFailoverHost || res.Spec.FailoverHostReservation == nil {
		return ctrl.Result{}, nil
	}
	log := logf.FromContext(ctx).WithName("failoverhost-completion").WithValues("reservation", res.Name)
	// Only InUse reservations are this controller's concern; the drain controller
	// and Matcher own everything up to InUse.
	if !meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
		return ctrl.Result{}, nil
	}
	sourceHost := ""
	if res.Status.FailoverHostReservation != nil {
		sourceHost = res.Status.FailoverHostReservation.SourceHost
	}
	if sourceHost == "" {
		log.V(1).Info("in-use reservation without a source host, skipping")
		return ctrl.Result{}, nil
	}

	// The source host reports itself evicted once its last instance has left: the
	// hypervisor operator sets status.evicted when no instances remain under active
	// maintenance mode. If the Hypervisor object is missing we cannot tell, so we
	// skip rather than assume the evacuation is done.
	hv := &hv1.Hypervisor{}
	if err := c.Get(ctx, client.ObjectKey{Name: sourceHost}, hv); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("source hypervisor not found, skipping until its status is known", "sourceHost", sourceHost)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get source hypervisor %s: %w", sourceHost, err)
	}
	sourceEvicted := hv.Status.Evicted
	completed := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)

	if !sourceEvicted {
		// Source not yet evicted: the evacuation is not done, or instances came
		// back. If we had already started the grace timer, reset it by flipping the
		// condition to False (which re-stamps lastTransitionTime).
		if completed != nil && completed.Status == metav1.ConditionTrue {
			log.Info("source host re-occupied before grace elapsed, resetting completion timer", "sourceHost", sourceHost)
			return ctrl.Result{}, c.setCompleted(ctx, res, metav1.ConditionFalse,
				v1alpha1.ReasonFailoverHostSourceReoccupied,
				"source host "+sourceHost+" is no longer evicted")
		}
		return ctrl.Result{}, nil
	}

	if completed == nil || completed.Status != metav1.ConditionTrue {
		log.Info("source host evicted, marking evacuation complete and starting grace period",
			"sourceHost", sourceHost, "gracePeriod", c.Config.GracePeriod.Duration)
		if err := c.setCompleted(ctx, res, metav1.ConditionTrue,
			v1alpha1.ReasonFailoverHostEvacuationComplete,
			"source host "+sourceHost+" reports itself evicted"); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue at the deadline so deletion fires even without a watch event.
		return ctrl.Result{RequeueAfter: c.Config.GracePeriod.Duration}, nil
	}

	graceLeft := c.Config.GracePeriod.Duration - time.Since(completed.LastTransitionTime.Time)
	if graceLeft > 0 {
		log.V(1).Info("grace period not yet elapsed, waiting", "graceLeft", graceLeft)
		return ctrl.Result{RequeueAfter: graceLeft}, nil
	}
	log.Info("grace period elapsed, deleting completed failover host reservation", "sourceHost", sourceHost)
	return ctrl.Result{}, c.deleteReservation(ctx, res)
}

// setCompleted writes the FailoverHostCompleted condition. meta.SetStatusCondition
// only re-stamps lastTransitionTime when the status actually flips, so repeated
// identical writes keep the grace-period anchor stable.
func (c *CompletionController) setCompleted(ctx context.Context, res *v1alpha1.Reservation, status metav1.ConditionStatus, reason, msg string) error {
	base := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeFailoverHostCompleted,
		Status:             status,
		ObservedGeneration: res.Generation,
		Reason:             reason,
		Message:            msg,
	})
	if err := c.Status().Patch(ctx, res, client.MergeFrom(base)); err != nil {
		c.Monitor.RecordReservationError("completion_mark")
		return fmt.Errorf("mark reservation %s completed=%s: %w", res.Name, status, err)
	}
	return nil
}

func (c *CompletionController) deleteReservation(ctx context.Context, res *v1alpha1.Reservation) error {
	if err := client.IgnoreNotFound(c.Delete(ctx, res)); err != nil {
		c.Monitor.RecordReservationError("completion_delete")
		return fmt.Errorf("delete reservation %s: %w", res.Name, err)
	}
	c.Monitor.RecordReservationDeleted()
	return nil
}

// SetupWithManager registers the controller. Besides failover host reservations
// it watches Hypervisors so that a source host flipping its evicted status
// re-evaluates the reservation that rescued it. Reconciles run one at a time.
func (c *CompletionController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	// The shared host index is idempotent; the drain controller may already have
	// registered it.
	if err := reservations.IndexReservationByHost(ctx, mcl); err != nil {
		return fmt.Errorf("index reservations by host: %w", err)
	}

	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(&v1alpha1.Reservation{},
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			res, ok := obj.(*v1alpha1.Reservation)
			return ok && res.Spec.Type == v1alpha1.ReservationTypeFailoverHost
		}),
	)
	if err != nil {
		return fmt.Errorf("watch reservations: %w", err)
	}

	// Only a change of the source host's evicted status affects completion, so
	// wake the InUse reservations that rescued the hypervisor when it flips.
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			return c.inUseReservationsForSourceHost(ctx, obj.GetName())
		}),
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldHV, okOld := e.ObjectOld.(*hv1.Hypervisor)
				newHV, okNew := e.ObjectNew.(*hv1.Hypervisor)
				return okOld && okNew && oldHV.Status.Evicted != newHV.Status.Evicted
			},
		},
	)
	if err != nil {
		return fmt.Errorf("watch hypervisors: %w", err)
	}

	return bldr.Named("failoverhost-completion-controller").
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

// inUseReservationsForSourceHost maps a host to the InUse failover host
// reservations whose SourceHost equals it. The shared reservation-by-host index
// keys on the spec target/source hosts, not the dynamic status SourceHost, so
// this lists failover reservations and filters on status instead.
func (c *CompletionController) inUseReservationsForSourceHost(ctx context.Context, host string) []reconcile.Request {
	if host == "" {
		return nil
	}
	list := &v1alpha1.ReservationList{}
	if err := c.List(ctx, list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost,
	}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list failover host reservations for source host", "host", host)
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		res := &list.Items[i]
		if res.Status.FailoverHostReservation != nil &&
			res.Status.FailoverHostReservation.SourceHost == host &&
			meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostInUse) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(res)})
		}
	}
	return reqs
}

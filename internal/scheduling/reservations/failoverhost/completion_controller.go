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
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

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
	// GracePeriod is how long the source host must stay empty (measured from the
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
// failover host reservations and, once the evacuated source host is empty, waits
// out a grace period before deleting the reservation. The deletion turns the
// reserved host back into a regular host, which keeps hosting the failed-over
// workload.
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
// rescued is empty it marks them FailoverHostCompleted and, after the grace
// period, deletes them.
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

	vms := &vmv1alpha1.VirtualMachineList{}
	if err := c.List(ctx, vms); err != nil {
		return ctrl.Result{}, fmt.Errorf("list virtual machines: %w", err)
	}
	// The evacuation is complete once no virtual machine remains on the source
	// host: every one has found a new home. A missing source hypervisor has no
	// VMs either, so it counts as empty and completes normally.
	sourceEmpty := !slices.ContainsFunc(vms.Items, func(vm vmv1alpha1.VirtualMachine) bool {
		return occupiedHost(&vm) == sourceHost
	})
	completed := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ConditionTypeFailoverHostCompleted)

	if !sourceEmpty {
		// Source still occupied: the evacuation is not done, or VMs came back. If
		// we had already started the grace timer, reset it by flipping the
		// condition to False (which re-stamps lastTransitionTime).
		if completed != nil && completed.Status == metav1.ConditionTrue {
			log.Info("source host re-occupied before grace elapsed, resetting completion timer", "sourceHost", sourceHost)
			return ctrl.Result{}, c.setCompleted(ctx, res, metav1.ConditionFalse,
				v1alpha1.ReasonFailoverHostSourceReoccupied,
				"virtual machines are present on the source host "+sourceHost+" again")
		}
		return ctrl.Result{}, nil
	}

	if completed == nil || completed.Status != metav1.ConditionTrue {
		log.Info("source host empty, marking evacuation complete and starting grace period",
			"sourceHost", sourceHost, "gracePeriod", c.Config.GracePeriod.Duration)
		if err := c.setCompleted(ctx, res, metav1.ConditionTrue,
			v1alpha1.ReasonFailoverHostEvacuationComplete,
			"no virtual machines remain on the source host "+sourceHost); err != nil {
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
// it watches VirtualMachines so that a VM leaving (or re-entering) a reservation's
// source host re-evaluates that reservation. Reconciles run one at a time.
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

	// Only a change of the host a VM occupies affects source-host emptiness. On
	// update both the old and new object are mapped, so the vacated host is woken
	// too.
	bldr, err = bldr.WatchesMulticluster(&vmv1alpha1.VirtualMachine{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			vm, ok := obj.(*vmv1alpha1.VirtualMachine)
			if !ok {
				return nil
			}
			return c.inUseReservationsForSourceHost(ctx, occupiedHost(vm))
		}),
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldVM, okOld := e.ObjectOld.(*vmv1alpha1.VirtualMachine)
				newVM, okNew := e.ObjectNew.(*vmv1alpha1.VirtualMachine)
				return okOld && okNew && occupiedHost(oldVM) != occupiedHost(newVM)
			},
		},
	)
	if err != nil {
		return fmt.Errorf("watch virtual machines: %w", err)
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

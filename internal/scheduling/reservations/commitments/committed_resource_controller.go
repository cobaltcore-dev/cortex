// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
)

const crFinalizer = "committed-resource.reservations.cortex.cloud/cleanup"

// CommittedResourceController reconciles CommittedResource CRDs and owns all child Reservation CRUD.
type CommittedResourceController struct {
	client.Client
	Scheme *runtime.Scheme
	Conf   Config
}

func (r *CommittedResourceController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr v1alpha1.CommittedResource
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctx = WithNewGlobalRequestID(ctx)
	logger := LoggerFromContext(ctx).WithValues(
		"component", "committed-resource-controller",
		"committedResource", req.Name,
	)

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, logger, &cr)
	}

	if !controllerutil.ContainsFinalizer(&cr, crFinalizer) {
		controllerutil.AddFinalizer(&cr, crFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	switch cr.Spec.State {
	case v1alpha1.CommitmentStatusPlanned:
		return ctrl.Result{}, r.setNotReady(ctx, &cr, "Planned", "commitment is not yet active")
	case v1alpha1.CommitmentStatusPending:
		return r.reconcilePending(ctx, logger, &cr)
	case v1alpha1.CommitmentStatusGuaranteed, v1alpha1.CommitmentStatusConfirmed:
		return r.reconcileCommitted(ctx, logger, &cr)
	case v1alpha1.CommitmentStatusSuperseded, v1alpha1.CommitmentStatusExpired:
		return r.reconcileInactive(ctx, logger, &cr)
	default:
		logger.Info("unknown commitment state, skipping", "state", cr.Spec.State)
		return ctrl.Result{}, nil
	}
}

// reconcilePending handles a one-shot confirmation attempt (Limes state: pending).
// If placement fails for any reason, all partial reservations are removed and the
// CR is marked Rejected so the HTTP API can report the outcome back to Limes.
func (r *CommittedResourceController) reconcilePending(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	if applyErr := r.applyReservationState(ctx, logger, cr); applyErr != nil {
		logger.Error(applyErr, "pending commitment placement failed, rejecting")
		if rollbackErr := r.deleteChildReservations(ctx, cr); rollbackErr != nil {
			logger.Error(rollbackErr, "rollback failed")
		}
		return ctrl.Result{}, r.setNotReady(ctx, cr, "Rejected", applyErr.Error())
	}
	return ctrl.Result{}, r.setAccepted(ctx, cr)
}

func (r *CommittedResourceController) reconcileCommitted(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	// Spec errors are permanent regardless of AllowRejection — a bad spec won't fix itself.
	if _, err := FromCommittedResource(*cr); err != nil {
		logger.Error(err, "invalid commitment spec, rejecting")
		return ctrl.Result{}, r.setNotReady(ctx, cr, "Rejected", err.Error())
	}
	if applyErr := r.applyReservationState(ctx, logger, cr); applyErr != nil {
		if cr.Spec.AllowRejection {
			logger.Error(applyErr, "committed placement failed, rolling back to accepted amount")
			if rollbackErr := r.rollbackToAccepted(ctx, logger, cr); rollbackErr != nil {
				logger.Error(rollbackErr, "rollback failed")
			}
			return ctrl.Result{}, r.setNotReady(ctx, cr, "Rejected", applyErr.Error())
		}
		logger.Error(applyErr, "committed placement incomplete, will retry", "requeueAfter", r.Conf.RequeueIntervalRetry)
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry}, r.setNotReady(ctx, cr, "Reserving", applyErr.Error())
	}
	return ctrl.Result{}, r.setAccepted(ctx, cr)
}

func (r *CommittedResourceController) applyReservationState(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) error {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return fmt.Errorf("flavor knowledge not ready: %w", err)
	}

	state, err := FromCommittedResource(*cr)
	if err != nil {
		return fmt.Errorf("invalid commitment spec: %w", err)
	}
	state.NamePrefix = cr.Name + "-"
	state.CreatorRequestID = reservations.GlobalRequestIDFromContext(ctx)

	result, err := NewReservationManager(r.Client).ApplyCommitmentState(ctx, logger, state, flavorGroups, "committed-resource-controller")
	if err != nil {
		return err
	}
	logger.Info("commitment state applied", "created", result.Created, "deleted", result.Deleted, "repaired", result.Repaired)
	return nil
}

func (r *CommittedResourceController) setAccepted(ctx context.Context, cr *v1alpha1.CommittedResource) error {
	now := metav1.Now()
	old := cr.DeepCopy()
	acceptedAmount := cr.Spec.Amount.DeepCopy()
	cr.Status.AcceptedAmount = &acceptedAmount
	cr.Status.AcceptedAt = &now
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Accepted",
		Message:            "commitment successfully reserved",
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, cr, client.MergeFrom(old)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func (r *CommittedResourceController) reconcileInactive(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	if err := r.deleteChildReservations(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("commitment inactive, child reservations removed", "state", cr.Spec.State)
	return ctrl.Result{}, r.setNotReady(ctx, cr, string(cr.Spec.State), "commitment is no longer active")
}

func (r *CommittedResourceController) reconcileDeletion(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	if err := r.deleteChildReservations(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(cr, crFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger.Info("committed resource deleted, child reservations cleaned up")
	return ctrl.Result{}, nil
}

// deleteChildReservations deletes all Reservation CRDs owned by this CommittedResource,
// identified by matching CommitmentUUID in the reservation spec.
func (r *CommittedResourceController) deleteChildReservations(ctx context.Context, cr *v1alpha1.CommittedResource) error {
	var list v1alpha1.ReservationList
	if err := r.List(ctx, &list, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		return fmt.Errorf("failed to list reservations: %w", err)
	}
	for i := range list.Items {
		res := &list.Items[i]
		if res.Spec.CommittedResourceReservation == nil ||
			res.Spec.CommittedResourceReservation.CommitmentUUID != cr.Spec.CommitmentUUID {
			continue
		}
		if err := r.Delete(ctx, res); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete reservation %s: %w", res.Name, err)
		}
	}
	return nil
}

// rollbackToAccepted restores child Reservations to match Status.AcceptedAmount.
// If AcceptedAmount is nil (new CR that was never accepted), all child Reservations are deleted.
func (r *CommittedResourceController) rollbackToAccepted(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) error {
	if cr.Status.AcceptedAmount == nil {
		return r.deleteChildReservations(ctx, cr)
	}
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		// Can't compute the rollback target — fall back to full delete rather than leaving
		// a partial state that's inconsistent with the unknown AcceptedAmount.
		logger.Error(err, "flavor knowledge unavailable during rollback, deleting all child reservations")
		return r.deleteChildReservations(ctx, cr)
	}
	state, err := FromCommittedResource(*cr)
	if err != nil {
		logger.Error(err, "invalid spec during rollback, deleting all child reservations")
		return r.deleteChildReservations(ctx, cr)
	}
	state.TotalMemoryBytes = cr.Status.AcceptedAmount.Value()
	state.NamePrefix = cr.Name + "-"
	state.CreatorRequestID = reservations.GlobalRequestIDFromContext(ctx)
	if _, err := NewReservationManager(r.Client).ApplyCommitmentState(ctx, logger, state, flavorGroups, "committed-resource-controller-rollback"); err != nil {
		return fmt.Errorf("rollback apply failed: %w", err)
	}
	return nil
}

// setNotReady patches Ready=False on CommittedResource status.
func (r *CommittedResourceController) setNotReady(ctx context.Context, cr *v1alpha1.CommittedResource, reason, message string) error {
	old := cr.DeepCopy()
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Patch(ctx, cr, client.MergeFrom(old)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommittedResourceController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	ctx := context.Background()
	if err := IndexFields(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up field indexes: %w", err)
	}

	bldr := multicluster.BuildController(mcl, mgr)
	var err error
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResource{},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}
	// Re-enqueue the parent CommittedResource when a child Reservation changes (e.g. external deletion).
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.Reservation{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil {
				return nil
			}
			uuid := res.Spec.CommittedResourceReservation.CommitmentUUID
			var crList v1alpha1.CommittedResourceList
			if err := r.List(ctx, &crList, client.MatchingFields{idxCommittedResourceByUUID: uuid}); err != nil {
				LoggerFromContext(ctx).Error(err, "failed to list CommittedResources by UUID", "uuid", uuid)
				return nil
			}
			if len(crList.Items) == 0 {
				return nil
			}
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: crList.Items[0].Name}}}
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("committed-resource").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

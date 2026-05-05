// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"time"

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

const (
	crFinalizer = "committed-resource.reservations.cortex.cloud/cleanup"

	// maxConsecutiveFailuresForSlowdown is the ConsecutiveFailures threshold above which the
	// Reservation watch stops re-enqueuing this CR. Without this guard, a broken rollback creates
	// reservation churn that bypasses RequeueAfter and keeps the controller in a tight retry loop.
	// The CR is still re-enqueued by RequeueAfter (with exponential backoff) and by spec changes.
	maxConsecutiveFailuresForSlowdown = 10
)

// CommittedResourceController reconciles CommittedResource CRDs and owns all child Reservation CRUD.
type CommittedResourceController struct {
	client.Client
	Scheme *runtime.Scheme
	Conf   CommittedResourceControllerConfig
}

func (r *CommittedResourceController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr v1alpha1.CommittedResource
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if creatorReq := cr.Annotations[v1alpha1.AnnotationCreatorRequestID]; creatorReq != "" {
		ctx = WithGlobalRequestID(ctx, creatorReq)
	} else {
		ctx = WithNewGlobalRequestID(ctx)
	}
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
		return ctrl.Result{}, r.setNotReady(ctx, &cr, v1alpha1.CommittedResourceReasonPlanned, "commitment is not yet active")
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

// reconcilePending handles a confirmation attempt (Limes state: pending).
// If AllowRejection=true (API path), placement failure marks the CR Rejected so the HTTP API
// can report the outcome back to Limes. If AllowRejection=false (syncer path), the controller
// retries with exponential backoff — Limes does not require confirmation for these transitions.
func (r *CommittedResourceController) reconcilePending(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	logger.Info("reconciling pending resource",
		"generation", cr.Generation,
		"az", cr.Spec.AvailabilityZone,
		"amount", cr.Spec.Amount.String(),
		"allowRejection", cr.Spec.AllowRejection,
	)
	// If this spec generation was already rejected, don't re-apply.
	// Without this guard the controller oscillates: apply bad spec → delete reservations →
	// Reservation watch re-enqueues → apply bad spec again → loop.
	if isRejectedForGeneration(cr) {
		logger.V(1).Info("spec already rejected for current generation", "generation", cr.Generation)
		return ctrl.Result{}, nil
	}
	result, applyErr := r.applyReservationState(ctx, logger, cr)
	if applyErr != nil {
		if cr.Spec.AllowRejection {
			logger.Error(applyErr, "pending commitment placement failed, rejecting")
			if rollbackErr := r.deleteChildReservations(ctx, cr); rollbackErr != nil {
				return ctrl.Result{}, rollbackErr
			}
			return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonRejected, applyErr.Error())
		}
		delay := r.retryDelay(cr)
		logger.Error(applyErr, "pending commitment placement failed, will retry", "requeueAfter", delay)
		return ctrl.Result{RequeueAfter: delay}, r.setNotReadyRetry(ctx, cr, applyErr.Error())
	}
	allReady, anyFailed, failReason, err := r.checkChildReservationStatus(ctx, cr, result.TotalSlots)
	if err != nil {
		return ctrl.Result{}, err
	}
	if anyFailed {
		if cr.Spec.AllowRejection {
			logger.Info("pending commitment rejected: reservation placement failed", "reason", failReason)
			if rollbackErr := r.deleteChildReservations(ctx, cr); rollbackErr != nil {
				return ctrl.Result{}, rollbackErr
			}
			return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonRejected, failReason)
		}
		delay := r.retryDelay(cr)
		logger.Info("pending commitment placement failed, will retry", "reason", failReason, "requeueAfter", delay)
		return ctrl.Result{RequeueAfter: delay}, r.setNotReadyRetry(ctx, cr, failReason)
	}
	if !allReady {
		// Reservation controller hasn't processed all slots yet; Reservation watch will re-enqueue.
		return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, "waiting for reservation placement")
	}
	logger.Info("committed resource accepted", "generation", cr.Generation, "amount", cr.Spec.Amount.String())
	return ctrl.Result{}, r.setAccepted(ctx, cr)
}

func (r *CommittedResourceController) reconcileCommitted(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (ctrl.Result, error) {
	logger.Info("reconciling committed resource",
		"generation", cr.Generation,
		"az", cr.Spec.AvailabilityZone,
		"amount", cr.Spec.Amount.String(),
		"allowRejection", cr.Spec.AllowRejection,
	)
	// If this spec generation was already rejected, maintain rollback state without re-applying.
	// Without this guard the controller oscillates: apply bad spec → rollback →
	// Reservation watch re-enqueues → apply bad spec again → loop.
	if isRejectedForGeneration(cr) {
		logger.V(1).Info("spec already rejected for current generation, maintaining rollback state", "generation", cr.Generation)
		return ctrl.Result{}, r.rollbackToAccepted(ctx, logger, cr)
	}
	// Spec errors are permanent regardless of AllowRejection — a bad spec won't fix itself.
	if _, err := FromCommittedResource(*cr); err != nil {
		logger.Error(err, "invalid commitment spec, rejecting")
		return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonRejected, err.Error())
	}
	result, applyErr := r.applyReservationState(ctx, logger, cr)
	if applyErr != nil {
		if cr.Spec.AllowRejection {
			logger.Error(applyErr, "committed placement failed, rolling back to accepted spec")
			if rollbackErr := r.rollbackToAccepted(ctx, logger, cr); rollbackErr != nil {
				return ctrl.Result{}, rollbackErr
			}
			return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonRejected, applyErr.Error())
		}
		delay := r.retryDelay(cr)
		logger.Error(applyErr, "committed placement incomplete, will retry", "requeueAfter", delay)
		return ctrl.Result{RequeueAfter: delay}, r.setNotReadyRetry(ctx, cr, applyErr.Error())
	}
	allReady, anyFailed, failReason, err := r.checkChildReservationStatus(ctx, cr, result.TotalSlots)
	if err != nil {
		return ctrl.Result{}, err
	}
	if anyFailed {
		if cr.Spec.AllowRejection {
			logger.Info("committed placement failed, rolling back to accepted spec", "reason", failReason)
			if rollbackErr := r.rollbackToAccepted(ctx, logger, cr); rollbackErr != nil {
				return ctrl.Result{}, rollbackErr
			}
			return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonRejected, failReason)
		}
		delay := r.retryDelay(cr)
		logger.Info("committed placement failed, will retry", "reason", failReason, "requeueAfter", delay)
		return ctrl.Result{RequeueAfter: delay}, r.setNotReadyRetry(ctx, cr, failReason)
	}
	if !allReady {
		// Reservation controller hasn't processed all slots yet; Reservation watch will re-enqueue.
		return ctrl.Result{}, r.setNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, "waiting for reservation placement")
	}
	logger.Info("committed resource accepted", "generation", cr.Generation, "amount", cr.Spec.Amount.String())
	return ctrl.Result{}, r.setAccepted(ctx, cr)
}

func (r *CommittedResourceController) applyReservationState(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) (*ApplyResult, error) {
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("flavor knowledge not ready: %w", err)
	}

	state, err := FromCommittedResource(*cr)
	if err != nil {
		return nil, fmt.Errorf("invalid commitment spec: %w", err)
	}
	state.NamePrefix = cr.Name + "-"
	state.CreatorRequestID = reservations.GlobalRequestIDFromContext(ctx)
	state.ParentGeneration = cr.Generation

	result, err := NewReservationManager(r.Client).ApplyCommitmentState(ctx, logger, state, flavorGroups, "committed-resource-controller")
	if err != nil {
		return nil, err
	}
	logger.Info("commitment state applied", "created", result.Created, "deleted", result.Deleted, "repaired", result.Repaired)
	return result, nil
}

// checkChildReservationStatus inspects the Ready conditions of all child Reservations for cr.
// Returns allReady=true when every child has Ready=True.
// Returns anyFailed=true (and the first failure message) when any child has Ready=False.
// Returns allReady=false, anyFailed=false when some children have no condition yet (placement pending).
func (r *CommittedResourceController) checkChildReservationStatus(ctx context.Context, cr *v1alpha1.CommittedResource, expectedSlots int) (allReady, anyFailed bool, failReason string, err error) {
	var list v1alpha1.ReservationList
	if err := r.List(ctx, &list,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
		client.MatchingFields{idxReservationByCommitmentUUID: cr.Spec.CommitmentUUID},
	); err != nil {
		return false, false, "", fmt.Errorf("failed to list reservations: %w", err)
	}

	// Cache hasn't caught up yet; Reservation watch will re-enqueue.
	if len(list.Items) < expectedSlots {
		return false, false, "", nil
	}

	if len(list.Items) == 0 {
		return true, false, "", nil
	}

	// First pass: failures take priority over pending — but only for the current generation.
	// A Ready=False condition from a previous generation means the reservation controller
	// hasn't reprocessed this slot yet; treat it as still-pending, not as a current failure.
	for _, res := range list.Items {
		if res.Status.CommittedResourceReservation == nil ||
			res.Status.CommittedResourceReservation.ObservedParentGeneration != cr.Generation {
			continue
		}
		cond := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
		if cond != nil && cond.Status == metav1.ConditionFalse {
			return false, true, cond.Message, nil
		}
	}
	// Second pass: check generation and readiness for all slots.
	for _, res := range list.Items {
		// ObservedParentGeneration must match cr.Generation before we trust the Ready condition.
		if res.Status.CommittedResourceReservation == nil ||
			res.Status.CommittedResourceReservation.ObservedParentGeneration != cr.Generation {
			return false, false, "", nil
		}
		cond := meta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			return false, false, "", nil
		}
	}
	return true, false, "", nil
}

func (r *CommittedResourceController) setAccepted(ctx context.Context, cr *v1alpha1.CommittedResource) error {
	now := metav1.Now()
	old := cr.DeepCopy()
	acceptedAmount := cr.Spec.Amount.DeepCopy()
	specCopy := cr.Spec.DeepCopy()
	cr.Status.AcceptedAmount = &acceptedAmount
	cr.Status.AcceptedSpec = specCopy
	cr.Status.ConsecutiveFailures = 0
	cr.Status.AcceptedAt = &now
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             v1alpha1.CommittedResourceReasonAccepted,
		Message:            "commitment successfully reserved",
		LastTransitionTime: now,
		ObservedGeneration: cr.Generation,
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
	if err := r.List(ctx, &list,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
		client.MatchingFields{idxReservationByCommitmentUUID: cr.Spec.CommitmentUUID},
	); err != nil {
		return fmt.Errorf("failed to list reservations: %w", err)
	}
	for i := range list.Items {
		res := &list.Items[i]
		if err := r.Delete(ctx, res); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete reservation %s: %w", res.Name, err)
		}
	}
	return nil
}

// rollbackToAccepted restores child Reservations to match Status.AcceptedSpec.
// AcceptedSpec is a full snapshot of the spec at the last successful reconcile, so rollback always
// targets the correct AZ, amount, project, domain — even when the current spec has been mutated.
// Falls back to AcceptedAmount + current spec fields for CRs accepted before AcceptedSpec existed.
// If neither is set (CR was never accepted), all child Reservations are deleted.
func (r *CommittedResourceController) rollbackToAccepted(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) error {
	if cr.Status.AcceptedSpec == nil && cr.Status.AcceptedAmount == nil {
		return r.deleteChildReservations(ctx, cr)
	}
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		// Can't compute the rollback target — fall back to full delete rather than leaving
		// a partial state that's inconsistent with the unknown accepted state.
		logger.Error(err, "flavor knowledge unavailable during rollback, deleting all child reservations")
		return r.deleteChildReservations(ctx, cr)
	}

	var state *CommitmentState
	if cr.Status.AcceptedSpec != nil {
		// Use the full accepted spec snapshot: ensures rollback targets the exact previously-accepted
		// placement (AZ, amount, project, domain) even if the current spec has been mutated.
		tempCR := v1alpha1.CommittedResource{Spec: *cr.Status.AcceptedSpec}
		state, err = FromCommittedResource(tempCR)
	} else {
		// Legacy fallback: AcceptedSpec not yet populated (CR accepted before this field existed).
		state, err = FromCommittedResource(*cr)
		if err == nil {
			state.TotalMemoryBytes = cr.Status.AcceptedAmount.Value()
		}
	}
	if err != nil {
		logger.Error(err, "invalid spec during rollback, deleting all child reservations")
		return r.deleteChildReservations(ctx, cr)
	}
	state.NamePrefix = cr.Name + "-"
	state.CreatorRequestID = reservations.GlobalRequestIDFromContext(ctx)
	state.ParentGeneration = cr.Generation
	if _, err := NewReservationManager(r.Client).ApplyCommitmentState(ctx, logger, state, flavorGroups, "committed-resource-controller-rollback"); err != nil {
		return fmt.Errorf("rollback apply failed: %w", err)
	}
	return nil
}

// isRejectedForGeneration returns true when the CR's Ready condition is already Rejected
// for the current spec generation. Used to short-circuit re-applying a spec that was
// already tried and rejected in a previous reconcile cycle.
func isRejectedForGeneration(cr *v1alpha1.CommittedResource) bool {
	cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	return cond != nil &&
		cond.Status == metav1.ConditionFalse &&
		cond.Reason == v1alpha1.CommittedResourceReasonRejected &&
		cond.ObservedGeneration == cr.Generation
}

// retryDelay computes an exponential backoff interval for the AllowRejection=false retry paths.
// Uses the pre-increment ConsecutiveFailures value: on the first failure (failures=0) the delay is
// base * 2^0 = base; setNotReadyRetry increments to 1 afterwards, so the second failure yields 2*base.
// The delay is capped at MaxRequeueInterval.
func (r *CommittedResourceController) retryDelay(cr *v1alpha1.CommittedResource) time.Duration {
	base := r.Conf.RequeueIntervalRetry.Duration
	exp := cr.Status.ConsecutiveFailures
	if exp > 6 {
		exp = 6 // overflow guard: 2^6 = 64 fits safely in uint; MaxRequeueInterval caps the actual duration
	}
	delay := base * time.Duration(uint64(1)<<uint(exp)) //nolint:gosec // exp is bounded to [0,6]
	if maxDelay := r.Conf.MaxRequeueInterval.Duration; maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

// setNotReady patches Ready=False on CommittedResource status.
func (r *CommittedResourceController) setNotReady(ctx context.Context, cr *v1alpha1.CommittedResource, reason, message string) error {
	return r.patchNotReady(ctx, cr, reason, message, false)
}

// setNotReadyRetry increments ConsecutiveFailures and patches Ready=False/Reserving.
// Use this in the AllowRejection=false retry paths so the failure counter drives backoff.
func (r *CommittedResourceController) setNotReadyRetry(ctx context.Context, cr *v1alpha1.CommittedResource, message string) error {
	return r.patchNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, message, true)
}

func (r *CommittedResourceController) patchNotReady(ctx context.Context, cr *v1alpha1.CommittedResource, reason, message string, countFailure bool) error {
	old := cr.DeepCopy()
	if countFailure {
		cr.Status.ConsecutiveFailures++
	}
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: cr.Generation,
	})
	if err := r.Status().Patch(ctx, cr, client.MergeFrom(old)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommittedResourceController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	ctx := context.Background()
	if err := indexReservationByCommitmentUUID(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up reservation field index: %w", err)
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
	// Suppressed when ConsecutiveFailures is high: a broken rollback creates reservation churn that
	// would bypass RequeueAfter and keep the controller in a tight loop. The Reservation watch is the
	// fast path for normal "waiting for placement" transitions; the RequeueAfter backoff handles retry.
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
			cr := &crList.Items[0]
			if cr.Status.ConsecutiveFailures >= maxConsecutiveFailuresForSlowdown {
				LoggerFromContext(ctx).V(1).Info("placement failures exceeded threshold, watch re-enqueues suppressed — retrying via backoff timer only",
					"name", cr.Name, "consecutiveFailures", cr.Status.ConsecutiveFailures, "threshold", maxConsecutiveFailuresForSlowdown)
				return nil
			}
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: cr.Name}}}
		}),
	)
	if err != nil {
		return err
	}
	// MaxConcurrentReconciles=1: the change-commitments API handler snapshots each CR's spec
	// before writing and restores it on rollback. Concurrent reconciles across overlapping
	// batch requests could interleave those snapshots and produce incorrect rollback state.
	return bldr.Named("committed-resource").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

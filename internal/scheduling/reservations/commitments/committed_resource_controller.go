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
	"sigs.k8s.io/controller-runtime/pkg/handler"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
)

const (
	// maxReservingAgeForSlowdown is how long a CR can be continuously in Reserving state
	// before the Reservation watch stops re-enqueuing it. Beyond this point only the
	// RequeueAfter backoff timer drives retries, preventing reservation churn from a broken
	// rollback from flooding the reconcile queue.
	maxReservingAgeForSlowdown = 30 * time.Minute
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
		return ctrl.Result{}, nil
	}

	// Treat time-expired CRs as inactive regardless of Spec.State.
	// The syncer updates State to expired on its own schedule (e.g. hourly); routing directly
	// to reconcileInactive here ensures reservation slots are deleted as soon as EndTime passes.
	if cr.Spec.EndTime != nil && cr.Spec.EndTime.Time.Before(time.Now()) {
		return r.reconcileInactive(ctx, logger, &cr)
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
		// Reset the retry timer: applyReservationState just succeeded, so the watch suppression
		// gate should not fire while we wait for slots to become ready.
		return ctrl.Result{}, r.patchNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, "waiting for reservation placement", true)
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
		// Reset the retry timer: applyReservationState just succeeded, so the watch suppression
		// gate should not fire while we wait for slots to become ready.
		return ctrl.Result{}, r.patchNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, "waiting for reservation placement", true)
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
	specCopy := cr.Spec.DeepCopy()
	cr.Status.AcceptedSpec = specCopy
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

// deleteChildReservations deletes all Reservation CRDs owned by this CommittedResource,
// identified by matching CommitmentUUID in the reservation spec.
func (r *CommittedResourceController) deleteChildReservations(ctx context.Context, cr *v1alpha1.CommittedResource) error {
	return DeleteChildReservations(ctx, r.Client, cr)
}

// DeleteChildReservations deletes all Reservation CRDs belonging to cr, matched by CommitmentUUID.
// Called both by the controller on inactive/rollback transitions and by the API handler on CR deletion.
func DeleteChildReservations(ctx context.Context, k8sClient client.Client, cr *v1alpha1.CommittedResource) error {
	var list v1alpha1.ReservationList
	if err := k8sClient.List(ctx, &list,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
		client.MatchingFields{idxReservationByCommitmentUUID: cr.Spec.CommitmentUUID},
	); err != nil {
		return fmt.Errorf("failed to list reservations: %w", err)
	}
	for i := range list.Items {
		res := &list.Items[i]
		if err := k8sClient.Delete(ctx, res); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete reservation %s: %w", res.Name, err)
		}
	}
	return nil
}

// rollbackToAccepted restores child Reservations to match Status.AcceptedSpec.
// AcceptedSpec is a full snapshot of the spec at the last successful reconcile, so rollback always
// targets the correct AZ, amount, project, domain — even when the current spec has been mutated.
// If AcceptedSpec is nil (CR was never accepted), all child Reservations are deleted.
func (r *CommittedResourceController) rollbackToAccepted(ctx context.Context, logger logr.Logger, cr *v1alpha1.CommittedResource) error {
	if cr.Status.AcceptedSpec == nil {
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
	// Use the full accepted spec snapshot: ensures rollback targets the exact previously-accepted
	// placement (AZ, amount, project, domain) even if the current spec has been mutated.
	tempCR := v1alpha1.CommittedResource{Spec: *cr.Status.AcceptedSpec}
	state, err = FromCommittedResource(tempCR)
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
// The exponent is derived from the time already spent in Reserving state: each doubling period
// advances one step, giving the same base→2*base→4*base→… sequence as a counter would, but
// without storing a raw count in status.
// The delay is capped at MaxRequeueInterval.
func (r *CommittedResourceController) retryDelay(cr *v1alpha1.CommittedResource) time.Duration {
	base := r.Conf.RequeueIntervalRetry.Duration
	cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil || cond.Reason != v1alpha1.CommittedResourceReasonReserving {
		return base
	}
	elapsed := time.Since(cond.LastTransitionTime.Time)
	var exp uint
	for step := base; elapsed >= step && exp < 6; exp++ {
		step *= 2
	}
	delay := base * time.Duration(uint64(1)<<exp) //nolint:gosec // exp is bounded to [0,6]
	if maxDelay := r.Conf.MaxRequeueInterval.Duration; maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

// setNotReady patches Ready=False on CommittedResource status.
func (r *CommittedResourceController) setNotReady(ctx context.Context, cr *v1alpha1.CommittedResource, reason, message string) error {
	return r.patchNotReady(ctx, cr, reason, message, false)
}

// setNotReadyRetry patches Ready=False/Reserving for the AllowRejection=false retry paths.
// The retry timer is not reset so that the elapsed time in Reserving state continues to
// drive the exponential backoff in retryDelay.
func (r *CommittedResourceController) setNotReadyRetry(ctx context.Context, cr *v1alpha1.CommittedResource, message string) error {
	return r.patchNotReady(ctx, cr, v1alpha1.CommittedResourceReasonReserving, message, false)
}

// patchNotReady patches Ready=False with reason/message.
// resetTimer=true forces LastTransitionTime to be refreshed even if reason is unchanged —
// use this on the "apply succeeded, waiting for slots" path so the watch suppression gate
// does not fire while placement is working.
func (r *CommittedResourceController) patchNotReady(ctx context.Context, cr *v1alpha1.CommittedResource, reason, message string, resetTimer bool) error {
	old := cr.DeepCopy()
	setReadyConditionFalse(&cr.Status.Conditions, reason, message, cr.Generation, resetTimer)
	if err := r.Status().Patch(ctx, cr, client.MergeFrom(old)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// setReadyConditionFalse sets Ready=False with the given reason/message.
// Unlike meta.SetStatusCondition, it refreshes LastTransitionTime whenever Status OR Reason
// changes, so retryDelay and the watch suppression gate always measure time-in-current-reason.
// resetTimer forces a refresh even when reason is unchanged (use on the "apply succeeded,
// waiting for slots" path to clear the failure history).
func setReadyConditionFalse(conditions *[]metav1.Condition, reason, message string, generation int64, resetTimer bool) {
	now := metav1.Now()
	for i, c := range *conditions {
		if c.Type != v1alpha1.CommittedResourceConditionReady {
			continue
		}
		newCond := metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
		}
		if !resetTimer && c.Status == metav1.ConditionFalse && c.Reason == reason {
			newCond.LastTransitionTime = c.LastTransitionTime
		} else {
			newCond.LastTransitionTime = now
		}
		(*conditions)[i] = newCond
		return
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               v1alpha1.CommittedResourceConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: now,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommittedResourceController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	ctx := context.Background()
	if err := indexReservationByCommitmentUUID(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up reservation field index: %w", err)
	}
	// Also register idxCommittedResourceByUUID here: the Reservation watch handler uses it to map
	// Reservation→CR. The UsageReconciler registers the same index, but it may not be set up when
	// commitmentsUsageDB is unconfigured. The once-guard in field_index.go makes this idempotent.
	if err := indexCommittedResourceByUUID(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up committed resource field index: %w", err)
	}
	if err := indexCommittedResourceByProjectID(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up committed resource project index: %w", err)
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
	// Suppressed when the CR has been continuously in Reserving state for longer than
	// maxReservingAgeForSlowdown: a broken rollback creates reservation churn that would bypass
	// RequeueAfter and keep the controller in a tight loop. The Reservation watch is the fast path
	// for normal "waiting for placement" transitions; the RequeueAfter backoff handles retry.
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
			// Suppress fast-path re-enqueues only when the reservation belongs to the current
			// generation AND the CR has been in Reserving state for too long. A new spec (higher
			// generation) gets a fresh start.
			readyCond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
			if readyCond != nil &&
				readyCond.Reason == v1alpha1.CommittedResourceReasonReserving &&
				readyCond.ObservedGeneration == cr.Generation &&
				res.Spec.CommittedResourceReservation.ParentGeneration == cr.Generation &&
				time.Since(readyCond.LastTransitionTime.Time) > maxReservingAgeForSlowdown {
				LoggerFromContext(ctx).V(1).Info("Reserving state age exceeded threshold, watch re-enqueues suppressed — retrying via backoff timer only",
					"name", cr.Name, "reservingAge", time.Since(readyCond.LastTransitionTime.Time).Round(time.Second), "threshold", maxReservingAgeForSlowdown)
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

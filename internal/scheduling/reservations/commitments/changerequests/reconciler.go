// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package changerequests

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
)

// CommittedResourceChangeRequestReconciler reconciles CommittedResourceChangeRequest CRDs.
// It is the sole writer of CommittedResource.Spec; it reads CommittedResource.Status
// to drive its own FSM. It never writes CommittedResource.Status.
//
// TODO (#783): add perAZActive map[string]types.UID + mu sync.Mutex for per-AZ serialization.
type CommittedResourceChangeRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// AwaitingSchedulingTimeout is how long to wait in AwaitingScheduling before rolling back.
	// Zero (default) means no timeout.
	AwaitingSchedulingTimeout time.Duration
}

func (r *CommittedResourceChangeRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = commitments.WithNewGlobalRequestID(ctx)
	logger := commitments.LoggerFromContext(ctx).WithValues("committedResourceChangeRequest", req.Name)

	var tx v1alpha1.CommittedResourceChangeRequest
	if err := r.Get(ctx, req.NamespacedName, &tx); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tx.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("reconcile entry", "rv", tx.ResourceVersion, "gen", tx.Generation, "phase", tx.Status.Phase)

	switch tx.Status.Phase {
	case "", v1alpha1.ChangeRequestPhasePreparing:
		return r.reconcilePreparing(ctx, logger, &tx)
	case v1alpha1.ChangeRequestPhaseAllocating:
		return r.reconcileAllocating(ctx, logger, &tx)
	case v1alpha1.ChangeRequestPhaseAwaitingScheduling:
		return r.reconcileAwaitingScheduling(ctx, logger, &tx)
	case v1alpha1.ChangeRequestPhaseRollingBack:
		return r.reconcileRollingBack(ctx, logger, &tx)
	case v1alpha1.ChangeRequestPhaseDryRunCleanup:
		return r.reconcileDryRunCleanup(ctx, logger, &tx)
	case v1alpha1.ChangeRequestPhaseAccepted, v1alpha1.ChangeRequestPhaseRejected, v1alpha1.ChangeRequestPhaseSuperseded:
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, fmt.Errorf("unknown phase %q", tx.Status.Phase)
	}
}

// commitmentName returns the CommittedResource name for a given commitment UUID.
func commitmentName(uuid string) string { return "commitment-" + uuid }

// reconcilePreparing snapshots pre-change CR specs and advances to Allocating.
// Always snapshots regardless of AllowRejection — syncer-mode CRTs never roll back but the
// snapshot is cheap and keeps this method free of branching.
func (r *CommittedResourceChangeRequestReconciler) reconcilePreparing(ctx context.Context, logger logr.Logger, tx *v1alpha1.CommittedResourceChangeRequest) (ctrl.Result, error) {
	// Snapshot current CR specs for all keys in NewState so rollback can restore them.
	// nil entry = CR did not exist before = delete it on rollback.
	snapshot := make(map[string]*v1alpha1.CommittedResourceSpec, len(tx.Spec.NewState))
	for uuid := range tx.Spec.NewState {
		var cr v1alpha1.CommittedResource
		if err := r.Get(ctx, client.ObjectKey{Name: commitmentName(uuid)}, &cr); err != nil {
			if client.IgnoreNotFound(err) == nil {
				snapshot[uuid] = nil
				continue
			}
			logger.Error(err, "failed to snapshot CR", "commitmentUUID", uuid, "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID)
			r.markError(ctx, tx, fmt.Sprintf("failed to snapshot commitment-%s: %v", uuid, err))
			return ctrl.Result{}, err
		}
		snapshot[uuid] = cr.Spec.DeepCopy()
	}

	logger.Info("starting transaction", "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID, "newStateSize", len(tx.Spec.NewState), "allowRejection", tx.Spec.AllowRejection)
	return r.advanceTo(ctx, tx, v1alpha1.ChangeRequestPhaseAllocating, func(s *v1alpha1.CommittedResourceChangeRequestStatus) {
		s.SnapshotBefore = snapshot
	})
}

// reconcileAllocating writes CommittedResource specs from NewState and records upserted refs.
// API-mode: advances to AwaitingScheduling.
// Syncer-mode: advances directly to Accepted (apply-and-done, no scheduling gate).
//
// UpsertedCommitments and the phase advance must happen in one patch (via the mutate function)
// so that a crash between CR spec writes and status persistence does not leave AwaitingScheduling
// with an empty UpsertedCommitments list — which would incorrectly advance to Accepted.
func (r *CommittedResourceChangeRequestReconciler) reconcileAllocating(ctx context.Context, logger logr.Logger, tx *v1alpha1.CommittedResourceChangeRequest) (ctrl.Result, error) {
	var upserted []corev1.ObjectReference

	for uuid, desiredSpec := range tx.Spec.NewState {
		name := commitmentName(uuid)
		if desiredSpec == nil {
			toDelete := &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Name: name}}
			if err := r.Delete(ctx, toDelete); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete CR", "commitmentUUID", uuid, "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID)
				r.markError(ctx, tx, fmt.Sprintf("failed to delete commitment-%s: %v", uuid, err))
				return ctrl.Result{}, err
			}
			continue
		}

		cr := &v1alpha1.CommittedResource{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cr, func() error {
			cr.Spec = *desiredSpec
			return nil
		}); err != nil {
			logger.Error(err, "failed to upsert CR spec", "commitmentUUID", uuid, "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID)
			r.markError(ctx, tx, fmt.Sprintf("failed to upsert commitment-%s: %v", uuid, err))
			return ctrl.Result{}, err
		}
		upserted = append(upserted, corev1.ObjectReference{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "CommittedResource",
			Name:       name,
		})
	}

	// TODO (#782): syncer-mode recency check before advancing
	nextPhase := v1alpha1.ChangeRequestPhaseAwaitingScheduling
	if !tx.Spec.AllowRejection {
		nextPhase = v1alpha1.ChangeRequestPhaseAccepted
	}
	return r.advanceTo(ctx, tx, nextPhase, func(s *v1alpha1.CommittedResourceChangeRequestStatus) {
		s.UpsertedCommitments = upserted
	})
}

// reconcileAwaitingScheduling reads UpsertedCommitments and waits for placement settlement.
// Gates on each CR's ready-condition observedGeneration matching the generation written in Allocating.
// All CRs accepted → Accepted (or DryRunCleanup when DryRun=true).
// Any CR rejected → RollingBack.
// Not yet settled → wait for the CR watch to re-enqueue.
func (r *CommittedResourceChangeRequestReconciler) reconcileAwaitingScheduling(ctx context.Context, logger logr.Logger, tx *v1alpha1.CommittedResourceChangeRequest) (ctrl.Result, error) {
	allAccepted := true
	acceptedCount := 0
	for _, ref := range tx.Status.UpsertedCommitments {
		var cr v1alpha1.CommittedResource
		if err := r.Get(ctx, client.ObjectKey{Name: ref.Name}, &cr); err != nil {
			if client.IgnoreNotFound(err) == nil {
				// CR briefly unavailable — wait for the next watch event.
				allAccepted = false
				continue
			}
			logger.Error(err, "failed to get upserted CR", "cr", ref.Name, "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID)
			r.markError(ctx, tx, fmt.Sprintf("failed to get %s: %v", ref.Name, err))
			return ctrl.Result{}, err
		}

		cond := meta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil || cond.ObservedGeneration != cr.Generation {
			// CR controller hasn't caught up to the spec we wrote — wait.
			allAccepted = false
			continue
		}

		switch {
		case cond.Status == metav1.ConditionTrue && cond.Reason == v1alpha1.CommittedResourceReasonAccepted:
			// CR accepted — placement succeeded; keep iterating.
			acceptedCount++
		case cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha1.CommittedResourceReasonRejected:
			// Fail-fast: start rollback immediately on first rejection rather than waiting for all CRs
			// to settle — this unblocks the AZ sooner and the first rejection message is sufficient for diagnosis.
			logger.Info("CR rejected, rolling back", "cr", ref.Name, "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID, "message", cond.Message)
			return r.advanceTo(ctx, tx, v1alpha1.ChangeRequestPhaseRollingBack, func(s *v1alpha1.CommittedResourceChangeRequestStatus) {
				s.Message = fmt.Sprintf("CR %s rejected: %s", ref.Name, cond.Message)
			})
		default:
			// Still transitioning — wait.
			allAccepted = false
		}
	}

	if !allAccepted {
		logger.V(1).Info("awaiting scheduling", "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID, "ready", acceptedCount, "total", len(tx.Status.UpsertedCommitments))
		if r.AwaitingSchedulingTimeout > 0 {
			progressingCond := meta.FindStatusCondition(tx.Status.Conditions, v1alpha1.ChangeRequestConditionProgressing)
			if progressingCond != nil && time.Since(progressingCond.LastTransitionTime.Time) > r.AwaitingSchedulingTimeout {
				logger.Info("timed out waiting for scheduling, rolling back", "az", tx.Spec.AvailabilityZone, "correlationID", tx.Spec.CorrelationID, "timeout", r.AwaitingSchedulingTimeout)
				return r.advanceTo(ctx, tx, v1alpha1.ChangeRequestPhaseRollingBack, func(s *v1alpha1.CommittedResourceChangeRequestStatus) {
					s.Message = "timed out waiting for scheduling"
					meta.SetStatusCondition(&s.Conditions, metav1.Condition{
						Type:               v1alpha1.ChangeRequestConditionProgressing,
						Status:             metav1.ConditionFalse,
						Reason:             v1alpha1.ChangeRequestReasonTimedOut,
						Message:            "timed out waiting for scheduling",
						ObservedGeneration: tx.Generation,
					})
				})
			}
		}
		// Primary requeue is event-driven via the CR watch (crToChangeRequest).
		// RequeueAfter is a safety-net for missed watch events (reconnects, startup races).
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if tx.Spec.DryRun {
		return r.advanceTo(ctx, tx, v1alpha1.ChangeRequestPhaseDryRunCleanup, nil)
	}
	return r.advanceTo(ctx, tx, v1alpha1.ChangeRequestPhaseAccepted, nil)
}

// reconcileRollingBack converges affected CR specs back to the pre-change SnapshotBefore state.
// On restore complete: advances to Rejected.
// On permanent cleanup failure: advances to Rejected with reason RollbackFailed.
func (r *CommittedResourceChangeRequestReconciler) reconcileRollingBack(ctx context.Context, logger logr.Logger, tx *v1alpha1.CommittedResourceChangeRequest) (ctrl.Result, error) {
	// TODO (#780): for each entry in SnapshotBefore, converge CR spec back to snapshot value
	//   using SSA with fieldManager=txFieldManager.
	//   nil snapshot entry → CR did not exist before → delete it.
	//   Gate terminal transition on each CR confirming its spec reverted (check observedGeneration).
	return ctrl.Result{}, nil
}

// reconcileDryRunCleanup restores pre-change state after a dry-run would-accept outcome.
// Re-uses snapshot rollback logic; terminal phase is Accepted (not Rejected).
func (r *CommittedResourceChangeRequestReconciler) reconcileDryRunCleanup(ctx context.Context, logger logr.Logger, tx *v1alpha1.CommittedResourceChangeRequest) (ctrl.Result, error) {
	// TODO (#780): same convergence as reconcileRollingBack; terminal → advanceTo(Accepted, nil).
	return ctrl.Result{}, nil
}

// advanceTo updates the transaction status to the given phase.
// mutate is called (when non-nil) to allow phase advance and other status mutations
// (e.g. UpsertedCommitments, SnapshotBefore) to be persisted atomically in one Update call.
// Progressing is set to False on terminal phases to reflect that no further work is expected.
//
// Status().Update is used (not Patch) so that nil map values in SnapshotBefore and similar
// fields are preserved — JSON merge patch semantics treat null map values as deletions.
func (r *CommittedResourceChangeRequestReconciler) advanceTo(
	ctx context.Context,
	tx *v1alpha1.CommittedResourceChangeRequest,
	phase v1alpha1.ChangeRequestPhase,
	mutate func(*v1alpha1.CommittedResourceChangeRequestStatus),
) (ctrl.Result, error) {

	tx.Status.Phase = phase
	if mutate != nil {
		mutate(&tx.Status)
	}
	progressingStatus, progressingReason := metav1.ConditionTrue, "Advancing"
	if tx.IsTerminal() {
		progressingStatus, progressingReason = metav1.ConditionFalse, string(phase)
		meta.SetStatusCondition(&tx.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ChangeRequestConditionFinished,
			Status:             metav1.ConditionTrue,
			Reason:             string(phase),
			ObservedGeneration: tx.Generation,
		})
	}
	// Preserve a Progressing=False condition already stamped by the mutate func (e.g. TimedOut).
	// Terminal phases always overwrite to ensure Progressing=False/<terminal-phase>.
	existingProgressing := meta.FindStatusCondition(tx.Status.Conditions, v1alpha1.ChangeRequestConditionProgressing)
	if tx.IsTerminal() || existingProgressing == nil || existingProgressing.Status != metav1.ConditionFalse {
		meta.SetStatusCondition(&tx.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ChangeRequestConditionProgressing,
			Status:             progressingStatus,
			Reason:             progressingReason,
			ObservedGeneration: tx.Generation,
		})
	}
	if err := r.Status().Update(ctx, tx); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// markError stamps Progressing=False/Error on the transaction as a best-effort status update.
// The update error is logged but not returned — callers return the original error for requeue.
func (r *CommittedResourceChangeRequestReconciler) markError(ctx context.Context, tx *v1alpha1.CommittedResourceChangeRequest, msg string) {
	meta.SetStatusCondition(&tx.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ChangeRequestConditionProgressing,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ChangeRequestReasonError,
		Message:            msg,
		ObservedGeneration: tx.Generation,
	})
	if err := r.Status().Update(ctx, tx); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to mark error condition")
	}
}

// SetupWithManager registers the controller and configures watches.
// TODO (#783): set MaxConcurrentReconciles: 1 via WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
// TODO (#783): add predicate on WatchesMulticluster() to re-trigger same-AZ non-terminal siblings on terminal transition.
func (r *CommittedResourceChangeRequestReconciler) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	ctx := context.Background()
	if err := indexCRCRByUpsertedCR(ctx, mcl); err != nil {
		return fmt.Errorf("failed to set up CRCR-by-upserted-CR field index: %w", err)
	}
	bldr := multicluster.BuildController(mcl, mgr)
	var err error
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResourceChangeRequest{},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}
	// Re-enqueues this transaction when a CR it upserted settles placement.
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.CommittedResource{},
		handler.EnqueueRequestsFromMapFunc(r.crToChangeRequest),
	)
	if err != nil {
		return err
	}
	return bldr.Named("committed-resource-change-request").Complete(r)
}

// crToChangeRequest maps a CommittedResource watch event back to the transaction that upserted it.
func (r *CommittedResourceChangeRequestReconciler) crToChangeRequest(ctx context.Context, obj client.Object) []ctrl.Request {
	var txList v1alpha1.CommittedResourceChangeRequestList
	if err := r.List(ctx, &txList, client.MatchingFields{idxCRCRByUpsertedCR: obj.GetName()}); err != nil {
		commitments.LoggerFromContext(ctx).Error(err, "failed to list CRCRs by upserted CR", "cr", obj.GetName())
		return nil
	}
	reqs := make([]ctrl.Request, 0, len(txList.Items))
	for i := range txList.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&txList.Items[i])})
	}
	return reqs
}

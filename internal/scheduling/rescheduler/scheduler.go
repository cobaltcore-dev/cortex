// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// Scheduler schedules ReschedulingRequests. Modelled after the kube-scheduler,
// it plays two roles over a single struct:
//
//   - as a controller-runtime reconciler it watches ReschedulingRequests and
//     dispatches on status.state, acknowledging new requests and enqueueing them
//     into the SchedulingQueue (the producer side);
//   - as a worker it runs an inner loop (Run/scheduleNext) that pops requests
//     from the queue and performs the scheduling pass (the consumer side).
//
// For the walking skeleton the handlers and the scheduling pass are stubs: no
// real precheck, placement, holds or VM CRD writes. The real logic is delivered
// in later milestones.
type Scheduler struct {
	// Client for the kubernetes API.
	client.Client
	// Queue holds Acknowledged requests waiting to be scheduled.
	Queue SchedulingQueue
	// Placement runs candidate discovery and reservation-time placement.
	// Defaults to a stub.
	Placement PlacementBackend
	// CandidateSaturation caps the candidate count used when ordering entities
	// for reservation. When zero, DefaultCandidateSaturation is used.
	CandidateSaturation int
	// BlockedHostsTTL is how long a succeeded request keeps its host-block
	// before releasing it. A zero value releases the block immediately.
	BlockedHostsTTL time.Duration
	// APITimeout bounds each apiserver read and write. When zero,
	// DefaultAPITimeout is used.
	APITimeout time.Duration
	// placements is the internal channel over which scheduleNext requests initial
	// placement from the placement broker (see Run/place). Keeping placement
	// behind a channel decouples the scheduler from how placement is computed
	// (today HTTP, later in-process).
	placements chan placementCall
}

// placementCall is a single placement request with a channel for its reply.
type placementCall struct {
	req   PlacementRequest
	reply chan PlacementResult
}

// apiTimeout returns the configured per-call apiserver timeout, falling back to
// the default when unset.
func (s *Scheduler) apiTimeout() time.Duration {
	if s.APITimeout <= 0 {
		return DefaultAPITimeout.Duration
	}
	return s.APITimeout
}

// apiCtx derives a context bounding a single apiserver call, so a slow or hung
// read/write cannot wedge the single worker goroutine. The caller must cancel.
func (s *Scheduler) apiCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.apiTimeout())
}

// updateStatus applies mutate to rr and patches the status subresource. The
// mutation may change the state and, in later milestones, conditions, recorded
// moves and other status fields. Any state-change legality and field
// immutability are enforced by the CRD CEL validation rules at the apiserver,
// so an illegal change surfaces here as an admission error (which requeues and
// re-dispatches from fresh state). The patch carries an optimistic lock on the
// resourceVersion: by design each request has a single writer, so a conflict is
// never expected and is surfaced as an error (not retried) to flag an unexpected
// concurrent writer rather than silently losing the write.
func (s *Scheduler) updateStatus(ctx context.Context, rr *v1alpha1.ReschedulingRequest, mutate func()) error {
	old := rr.DeepCopy()
	mutate()
	ctx, cancel := s.apiCtx(ctx)
	defer cancel()
	return s.Status().Patch(ctx, rr, client.MergeFromWithOptions(old, client.MergeFromWithOptimisticLock{}))
}

// currentState returns the effective state of a request, treating the empty
// (unset) state as Pending to match the CRD default.
func currentState(rr *v1alpha1.ReschedulingRequest) v1alpha1.ReschedulingRequestState {
	if rr.Status.State == "" {
		return v1alpha1.ReschedulingRequestStatePending
	}
	return rr.Status.State
}

// reconcilable reports whether a request in the given state still needs
// reconciliation. Succeeded and Rejected are fully done; Failed stays
// reconcilable so its cleanup can run and be retried.
func reconcilable(state v1alpha1.ReschedulingRequestState) bool {
	switch state {
	case v1alpha1.ReschedulingRequestStateSucceeded, v1alpha1.ReschedulingRequestStateRejected:
		return false
	default:
		return true
	}
}

// needsReconcile reports whether a request should still be watched and
// reconciled. It extends reconcilable with the host-block grace period: a
// succeeded request that still holds a host-block must keep reconciling until
// the block is released.
func needsReconcile(rr *v1alpha1.ReschedulingRequest) bool {
	return reconcilable(currentState(rr)) || len(rr.Status.BlockedHosts) > 0
}

// enqueue adds a request to the scheduling queue.
func (s *Scheduler) enqueue(rr *v1alpha1.ReschedulingRequest) {
	s.Queue.Add(client.ObjectKeyFromObject(rr))
}

// Reconcile dispatches a ReschedulingRequest to the handler for its current
// state.
func (s *Scheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rr := &v1alpha1.ReschedulingRequest{}
	getCtx, cancel := s.apiCtx(ctx)
	defer cancel()
	if err := s.Get(getCtx, req.NamespacedName, rr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return s.dispatch(ctx, rr)
}

// dispatch routes a request to the handler for its current state.
func (s *Scheduler) dispatch(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("request", rr.Name, "state", currentState(rr))
	switch currentState(rr) {
	case v1alpha1.ReschedulingRequestStatePending:
		return s.handlePending(ctx, rr)
	case v1alpha1.ReschedulingRequestStateAcknowledged:
		return s.handleAcknowledged(ctx, rr)
	case v1alpha1.ReschedulingRequestStateProcessing:
		return s.handleProcessing(ctx, rr)
	case v1alpha1.ReschedulingRequestStateAccepted:
		return s.handleAccepted(ctx, rr)
	case v1alpha1.ReschedulingRequestStateSucceeded:
		return s.handleSucceeded(ctx, rr)
	case v1alpha1.ReschedulingRequestStateFailed:
		return s.handleFailed(ctx, rr)
	case v1alpha1.ReschedulingRequestStateRejected:
		// Fully terminal: no cleanup required, nothing to do.
		return ctrl.Result{}, nil
	default:
		log.Info("skipping request, unknown state")
		return ctrl.Result{}, nil
	}
}

// handlePending runs the (stubbed) precheck, acknowledges the request and
// enqueues it for scheduling. The real precheck that may reject a request is
// delivered in the scheduling core milestone.
func (s *Scheduler) handlePending(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("acknowledging rescheduling request", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.State = v1alpha1.ReschedulingRequestStateAcknowledged
	}); err != nil {
		return ctrl.Result{}, err
	}
	s.enqueue(rr)
	return ctrl.Result{}, nil
}

// handleAcknowledged ensures the request is queued for scheduling. It runs both
// in normal flow (right after handlePending acknowledges) and on restart, when
// the informer replays every Acknowledged request; so it is what rebuilds the
// scheduling queue after a restart. The Acknowledged -> Processing -> Accepted
// transitions are owned by the scheduler loop (scheduleOne), not the reconciler.
func (s *Scheduler) handleAcknowledged(_ context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	s.enqueue(rr)
	return ctrl.Result{}, nil
}

// handleProcessing is a stub. The Processing -> Accepted transition is owned by
// the scheduler loop (scheduleOne).
func (s *Scheduler) handleProcessing(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("processing handled by scheduler loop (stub)", "request", rr.Name)
	return ctrl.Result{}, nil
}

// handleAccepted is a stub for execution confirmation. It advances the request
// to Succeeded. The real Accepted -> Succeeded/Failed handling (owned by the
// external state watcher) is delivered later. If the request holds a host-block,
// its expiry is set so the block lingers for the configured grace period after
// the move completes.
func (s *Scheduler) handleAccepted(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("confirming rescheduling request (stub)", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		if len(rr.Status.BlockedHosts) > 0 {
			expiry := metav1.NewTime(time.Now().Add(s.BlockedHostsTTL))
			rr.Status.BlockExpiresAt = &expiry
		}
		rr.Status.State = v1alpha1.ReschedulingRequestStateSucceeded
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// handleSucceeded releases the host-block of a succeeded request once its grace
// period has passed. Until then it requeues so the block is held for the full
// TTL; a request without a block is fully terminal.
func (s *Scheduler) handleSucceeded(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	if len(rr.Status.BlockedHosts) == 0 {
		return ctrl.Result{}, nil // nothing blocked, fully terminal
	}
	if rr.Status.BlockExpiresAt != nil {
		if remaining := time.Until(rr.Status.BlockExpiresAt.Time); remaining > 0 {
			return ctrl.Result{RequeueAfter: remaining}, nil
		}
	}
	logf.FromContext(ctx).Info("releasing host-block of succeeded request", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.BlockedHosts = nil
		rr.Status.BlockExpiresAt = nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// handleFailed runs cleanup for a failed request. Failed is terminal in the
// state machine, but cleanup (releasing holds/reservations, etc.) may need to
// run and be retried until it succeeds, so this handler stays reconcilable
// rather than being a no-op. Today the only cleanup is releasing the host-block;
// releasing inflight holds is a no-op until inflight integration lands.
func (s *Scheduler) handleFailed(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	if len(rr.Status.BlockedHosts) == 0 {
		return ctrl.Result{}, nil
	}
	logf.FromContext(ctx).Info("cleaning up failed rescheduling request", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.BlockedHosts = nil
		rr.Status.BlockExpiresAt = nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// Run is the scheduler worker loop. It runs the queue and pops one request at a
// time in a dedicated goroutine, so scheduleNext can block on the next item
// without blocking shutdown. On context cancellation the queue is closed, which
// unblocks the pending Pop.
func (s *Scheduler) Run(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("rescheduler-scheduler")
	log.Info("starting scheduler loop")
	if s.placements == nil {
		s.placements = make(chan placementCall)
	}
	if s.Placement == nil {
		s.Placement = StubPlacementBackend{}
	}
	go s.runPlacement(ctx)
	s.Queue.Run(ctx)
	go wait.UntilWithContext(ctx, s.scheduleNext, 0)
	<-ctx.Done()
	s.Queue.Close()
	log.Info("scheduler loop stopped")
}

// runPlacement is the placement broker: it serves placement requests sent on the
// placements channel using the configured backend. It runs until ctx is done.
func (s *Scheduler) runPlacement(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case call := <-s.placements:
			call.reply <- s.Placement.Schedule(ctx, call.req)
		}
	}
}

// place requests a placement pipeline run over the channel and waits for the reply.
func (s *Scheduler) place(ctx context.Context, req PlacementRequest) PlacementResult {
	reply := make(chan PlacementResult, 1)
	select {
	case s.placements <- placementCall{req: req, reply: reply}:
	case <-ctx.Done():
		return PlacementResult{Err: ctx.Err()}
	}
	select {
	case res := <-reply:
		return res
	case <-ctx.Done():
		return PlacementResult{Err: ctx.Err()}
	}
}

// scheduleNext is the worker-loop consumer: it pops a single request from the
// queue, loads it, drops stale entries whose request is no longer Acknowledged,
// and hands a fresh Acknowledged request to scheduleOne for the scheduling pass.
func (s *Scheduler) scheduleNext(ctx context.Context) {
	log := logf.FromContext(ctx)
	key, ok := s.Queue.Pop()
	if !ok {
		return // queue closed
	}
	rr := &v1alpha1.ReschedulingRequest{}
	getCtx, cancel := s.apiCtx(ctx)
	defer cancel()
	if err := s.Get(getCtx, key, rr); err != nil {
		log.Error(err, "dropping queued request, failed to get it", "key", key.String())
		return
	}
	// The request may have moved on since it was queued (e.g. a stale entry).
	if currentState(rr) != v1alpha1.ReschedulingRequestStateAcknowledged {
		return
	}
	s.scheduleOne(ctx, rr)
}

// reconcileOnRestart recovers requests left mid-flight by a previous process.
// A request found in Processing is forced to Failed: its in-flight work was lost
// when the scheduler stopped, and the per-object reconcile cannot recover it
// (handleProcessing is a no-op, since during steady state Processing means the
// worker is actively running the pass and must not be disturbed). This runs once
// at startup, when any Processing request is necessarily leftover.
//
// Rebuilding the scheduling queue from Acknowledged requests is not done here:
// the informer delivers a reconcile for every request on startup, and
// handleAcknowledged re-enqueues them, so the queue is rebuilt by the normal
// reconcile path.
func (s *Scheduler) reconcileOnRestart(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("rescheduler-startup")
	var requests v1alpha1.ReschedulingRequestList
	listCtx, cancel := s.apiCtx(ctx)
	defer cancel()
	if err := s.List(listCtx, &requests); err != nil {
		log.Error(err, "failed to list rescheduling requests during startup")
		return err
	}
	failed := 0
	for i := range requests.Items {
		rr := &requests.Items[i]
		if currentState(rr) != v1alpha1.ReschedulingRequestStateProcessing {
			continue
		}
		if err := s.updateStatus(ctx, rr, func() {
			rr.Status.BlockedHosts = nil
			rr.Status.State = v1alpha1.ReschedulingRequestStateFailed
		}); err != nil {
			log.Error(err, "failed to fail interrupted request", "request", rr.Name)
			return err
		}
		failed++
	}
	log.Info("startup reconciliation completed", "requests", len(requests.Items), "failed", failed)
	return nil
}

// startupRunnable runs the restart reconciliation once when the manager starts.
type startupRunnable struct{ *Scheduler }

func (s *startupRunnable) Start(ctx context.Context) error {
	return s.reconcileOnRestart(ctx)
}

func (s *Scheduler) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	if s.Queue == nil {
		s.Queue = NewFIFOQueue()
	}
	// Fail requests left mid-flight (Processing) by a previous process.
	if err := mgr.Add(&startupRunnable{s}); err != nil {
		return err
	}
	// Run the scheduler worker loop as its own entry point.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		s.Run(ctx)
		return nil
	})); err != nil {
		return err
	}
	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(
		&v1alpha1.ReschedulingRequest{},
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			rr, ok := obj.(*v1alpha1.ReschedulingRequest)
			if !ok {
				return false
			}
			// Succeeded/Rejected need no further work, unless a succeeded
			// request still holds a host-block to release. Failed still
			// reconciles so its cleanup handler can run (and retry).
			return needsReconcile(rr)
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("cortex-rescheduler-controller").Complete(s)
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

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
//   - as a worker it runs an inner loop (Run/scheduleOne) that pops requests
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
	// Placement computes initial placement for a request. Defaults to a stub.
	Placement PlacementBackend
	// placements is the internal channel over which scheduleOne requests initial
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

// updateStatus applies mutate to rr and patches the status subresource. The
// mutation may change the state and, in later milestones, conditions, recorded
// moves and other status fields. Any state-change legality and field
// immutability are enforced by the CRD CEL validation rules at the apiserver,
// so an illegal change surfaces here as an admission error (which requeues and
// re-dispatches from fresh state).
func (s *Scheduler) updateStatus(ctx context.Context, rr *v1alpha1.ReschedulingRequest, mutate func()) error {
	old := rr.DeepCopy()
	mutate()
	return s.Status().Patch(ctx, rr, client.MergeFrom(old))
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

// enqueue adds a request to the scheduling queue.
func (s *Scheduler) enqueue(rr *v1alpha1.ReschedulingRequest) {
	s.Queue.Add(client.ObjectKeyFromObject(rr))
}

// Reconcile dispatches a ReschedulingRequest to the handler for its current
// state.
func (s *Scheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rr := &v1alpha1.ReschedulingRequest{}
	if err := s.Get(ctx, req.NamespacedName, rr); err != nil {
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
	case v1alpha1.ReschedulingRequestStateFailed:
		return s.handleFailed(ctx, rr)
	case v1alpha1.ReschedulingRequestStateRejected,
		v1alpha1.ReschedulingRequestStateSucceeded:
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

// handleAcknowledged ensures the request is queued for scheduling. The
// Acknowledged -> Processing -> Accepted transitions are owned by the scheduler
// loop (scheduleOne), not the reconciler.
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
// external state watcher) is delivered later.
func (s *Scheduler) handleAccepted(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("confirming rescheduling request (stub)", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.State = v1alpha1.ReschedulingRequestStateSucceeded
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// handleFailed runs cleanup for a failed request. Failed is terminal in the
// state machine, but cleanup (releasing holds/reservations, etc.) may need to
// run and be retried until it succeeds, so this handler stays reconcilable
// rather than being a no-op. The real cleanup is a stub for now.
func (s *Scheduler) handleFailed(ctx context.Context, rr *v1alpha1.ReschedulingRequest) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("cleaning up failed rescheduling request (stub)", "request", rr.Name)
	return ctrl.Result{}, nil
}

// Run is the scheduler worker loop. It runs the queue and pops one request at a
// time in a dedicated goroutine, so scheduleOne can block on the next item
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
	go wait.UntilWithContext(ctx, s.scheduleOne, 0)
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

// place requests initial placement over the channel and waits for the reply.
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

// scheduleOne pops a single request from the queue and runs the scheduling pass:
// it claims the request (Acknowledged -> Processing), asks initial placement for
// candidate hosts over the placement channel, and moves the request to Accepted
// (hosts found), Rejected (none) or Failed (placement error).
func (s *Scheduler) scheduleOne(ctx context.Context) {
	log := logf.FromContext(ctx)
	key, ok := s.Queue.Pop()
	if !ok {
		return // queue closed
	}
	rr := &v1alpha1.ReschedulingRequest{}
	if err := s.Get(ctx, key, rr); err != nil {
		log.Error(err, "dropping queued request, failed to get it", "key", key.String())
		return
	}
	// The request may have moved on since it was queued (e.g. a stale entry).
	if currentState(rr) != v1alpha1.ReschedulingRequestStateAcknowledged {
		return
	}
	log.Info("scheduling rescheduling request", "request", rr.Name)
	if err := s.updateStatus(ctx, rr, func() {
		rr.Status.State = v1alpha1.ReschedulingRequestStateProcessing
	}); err != nil {
		log.Error(err, "failed to mark request processing", "request", rr.Name)
		return
	}

	result := s.place(ctx, placementRequestFor(rr))
	switch {
	case result.Err != nil:
		log.Error(result.Err, "placement failed", "request", rr.Name)
		if err := s.updateStatus(ctx, rr, func() {
			rr.Status.State = v1alpha1.ReschedulingRequestStateFailed
		}); err != nil {
			log.Error(err, "failed to mark request failed", "request", rr.Name)
		}
	case len(result.Hosts) == 0:
		log.Info("placement found no host, rejecting request", "request", rr.Name)
		if err := s.updateStatus(ctx, rr, func() {
			rr.Status.State = v1alpha1.ReschedulingRequestStateRejected
		}); err != nil {
			log.Error(err, "failed to mark request rejected", "request", rr.Name)
		}
	default:
		if err := s.updateStatus(ctx, rr, func() {
			rr.Status.State = v1alpha1.ReschedulingRequestStateAccepted
		}); err != nil {
			log.Error(err, "failed to accept request", "request", rr.Name)
		}
	}
}

// placementRequestFor builds the initial-placement request for a rescheduling
// request. It is provisional: it carries what the walking skeleton knows (the
// availability zone and the hosts being drained); the real resource shape is
// filled in once data gathering lands.
func placementRequestFor(rr *v1alpha1.ReschedulingRequest) PlacementRequest {
	req := PlacementRequest{AvailabilityZone: rr.Spec.AvailabilityZone}
	if rr.Spec.Drain != nil {
		for _, host := range rr.Spec.Drain.Target.Hosts {
			req.IgnoreHosts = append(req.IgnoreHosts, host.Host)
		}
	}
	return req
}

// reconcileOnRestart rebuilds the in-memory state after a restart so that no
// request is left in an indeterminate state:
//   - Acknowledged requests are re-enqueued to rebuild the scheduling queue;
//   - Processing requests are forced to Failed, since their in-flight work was
//     lost when the scheduler stopped.
func (s *Scheduler) reconcileOnRestart(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("rescheduler-startup")
	var requests v1alpha1.ReschedulingRequestList
	if err := s.List(ctx, &requests); err != nil {
		log.Error(err, "failed to list rescheduling requests during startup")
		return err
	}
	enqueued, failed := 0, 0
	for i := range requests.Items {
		rr := &requests.Items[i]
		switch currentState(rr) {
		case v1alpha1.ReschedulingRequestStateAcknowledged:
			s.enqueue(rr)
			enqueued++
		case v1alpha1.ReschedulingRequestStateProcessing:
			if err := s.updateStatus(ctx, rr, func() {
				rr.Status.State = v1alpha1.ReschedulingRequestStateFailed
			}); err != nil {
				log.Error(err, "failed to fail interrupted request", "request", rr.Name)
				return err
			}
			failed++
		}
	}
	log.Info("startup reconciliation completed", "requests", len(requests.Items), "enqueued", enqueued, "failed", failed)
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
	// Rebuild the queue / fail interrupted requests on startup.
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
			// Succeeded/Rejected need no further work. Failed still reconciles
			// so its cleanup handler can run (and retry).
			return reconcilable(currentState(rr))
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("cortex-rescheduler-controller").Complete(s)
}

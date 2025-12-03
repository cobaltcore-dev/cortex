package task

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type Runner struct {
	Client   client.Client
	Interval time.Duration

	Init func(ctx context.Context) error
	Run  func(ctx context.Context) error

	eventCh chan event.GenericEvent
}

func (r *Runner) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Trigger a run of the task when an event is received
	if r.Run != nil {
		if err := r.Run(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *Runner) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	defer close(r.eventCh)

	if r.Init != nil {
		if err := r.Init(ctx); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ticker.C:
			// Send an event to trigger the task run
			r.eventCh <- event.GenericEvent{}
		case <-ctx.Done():
			return nil
		}
	}
}

func (r *Runner) SetupWithManager(mgr manager.Manager) error {
	r.eventCh = make(chan event.GenericEvent)
	handler := &handler.EnqueueRequestForObject{}
	src := source.Channel(r.eventCh, handler, nil)
	return ctrl.NewControllerManagedBy(mgr).
		WatchesRawSource(src).
		Complete(r)
}

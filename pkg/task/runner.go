// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Runner is a generic task runner that can be used to run periodic tasks
// in the controller-runtime manager. The runner acts as a controller that
// watches for generic events and triggers the task run on each event.
// In this way, it is properly integrated with the manager lifecycle and can
// leverage the controller-runtime features.
type Runner struct {
	// The kubernetes client to use.
	Client client.Client
	// The interval at which to run the task.
	Interval time.Duration
	// The name of the task.
	Name string

	// If set, this function is called once at the start of the runner.
	Init func(ctx context.Context) error
	// If set, this function is called on each task run.
	Run func(ctx context.Context) error

	// Internal channel to receive events to trigger the task run.
	eventCh chan event.GenericEvent
}

// Reconcile is called when an event is received to trigger the task run.
func (r *Runner) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("running task", "name", r.Name)
	// Trigger a run of the task when an event is received
	if r.Run != nil {
		if err := r.Run(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// Start starts the task runner, which will send events at the specified interval.
func (r *Runner) Start(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("starting task runner", "name", r.Name, "interval", r.Interval)

	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	defer close(r.eventCh)

	if r.Init != nil {
		if err := r.Init(ctx); err != nil {
			return err
		}
	}

	// We don't really care about the content of the event, but we can use
	// the kubernetes job resource as a dummy object to trigger the reconcile.
	r.eventCh <- event.GenericEvent{
		Object: &batchv1.Job{
			TypeMeta: v1.TypeMeta{
				Kind:       "Job",
				APIVersion: "batch/v1",
			},
			ObjectMeta: v1.ObjectMeta{Name: "initial-trigger"},
		},
	}
	for {
		select {
		case <-ticker.C:
			// Send an event to trigger the task run
			r.eventCh <- event.GenericEvent{
				Object: &batchv1.Job{
					TypeMeta: v1.TypeMeta{
						Kind:       "Job",
						APIVersion: "batch/v1",
					},
					ObjectMeta: v1.ObjectMeta{Name: "scheduled-trigger"},
				},
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// SetupWithManager sets up the task runner with the controller-runtime manager.
func (r *Runner) SetupWithManager(mgr manager.Manager) error {
	r.eventCh = make(chan event.GenericEvent)
	src := source.Channel(r.eventCh, &handler.EnqueueRequestForObject{})
	if err := mgr.Add(r); err != nil {
		return err
	}
	// We don't care where the resources are maintained, thus we don't need to
	// setup the multicluster client here. We only use the manager to run the
	// controller.
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.Name).
		WatchesRawSource(src).
		Complete(r)
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type DeschedulingsCleanupOnStartup struct{ *DeschedulingsCleanup }

// Cleanup all old deschedulings on controller startup.
func (s *DeschedulingsCleanupOnStartup) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("ttl-startup-reconciler")
	log.Info("starting descheduling cleanup for existing resources")
	var resources v1alpha1.DeschedulingList
	if err := s.List(ctx, &resources); err != nil {
		log.Error(err, "failed to list descheduling resources during startup reconciliation")
		return err
	}
	log.Info("processing existing resources for cleanup", "resourceCount", len(resources.Items))
	processed, deleted := 0, 0
	for _, resource := range resources.Items {
		// Use the shared cleanup logic
		result, err := s.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{
			Name:      resource.Name,
			Namespace: resource.Namespace,
		}})
		if err != nil {
			log.Error(err, "failed to process resource during startup reconciliation", "name", resource.Name)
		} else if result.RequeueAfter == 0 {
			// Resource was deleted (no requeue means it was expired and deleted)
			deleted++
		}
		processed++
	}
	log.Info(
		"startup cleanup reconciliation completed",
		"processed", processed, "deleted", deleted,
	)
	return nil
}

// Removes old deschedulings.
type DeschedulingsCleanup struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
}

func (r *DeschedulingsCleanup) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("cleanup")

	// Fetch the descheduling object
	var descheduling v1alpha1.Descheduling
	if err := r.Get(ctx, req.NamespacedName, &descheduling); err != nil {
		// Resource was deleted or doesn't exist - nothing to clean up
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the descheduling is >24h old and can be deleted.
	elapsed := time.Since(descheduling.CreationTimestamp.Time)
	if elapsed < 24*time.Hour {
		// Not old enough yet - requeue for later
		log.Info("descheduling not old enough yet to be cleaned up", "name", descheduling.Name, "age", elapsed.String())
		return ctrl.Result{RequeueAfter: 24*time.Hour - elapsed}, nil
	}

	log.Info("deleting old descheduling", "name", descheduling.Name, "age", elapsed.String())
	if err := r.Delete(ctx, &descheduling); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to delete old descheduling", "name", descheduling.Name)
			return ctrl.Result{}, err
		}
		log.Info("descheduling was already deleted", "name", descheduling.Name)
		return ctrl.Result{}, nil
	}

	log.Info("descheduling was deleted", "name", descheduling.Name)
	return ctrl.Result{}, nil
}

func (r *DeschedulingsCleanup) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	if err := mgr.Add(&DeschedulingsCleanupOnStartup{r}); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		For(&v1alpha1.Descheduling{}).
		Named("descheduler-cleanup").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		WithEventFilter(
			// Watch for spec changes (when decisions are added/modified)
			predicate.GenerationChangedPredicate{},
		).
		Complete(r)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	decisionsv1alpha1 "github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

// TTLStartupReconciler handles startup reconciliation for existing resources
type TTLStartupReconciler struct {
	ttlController *SchedulingDecisionTTLController
}

// Start implements the Runnable interface and runs startup reconciliation
func (s *TTLStartupReconciler) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("ttl-startup-reconciler")
	log.Info("Starting TTL startup reconciliation for existing resources")

	s.ttlController.reconcileAllResourcesOnStartup(ctx)
	return nil
}

// SchedulingDecisionTTLController handles automatic cleanup of resolved SchedulingDecision resources
// after a configurable TTL period.
type SchedulingDecisionTTLController struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the decisions.
	Scheme *runtime.Scheme
	// Configuration for the TTL controller.
	Conf Config
}

// +kubebuilder:rbac:groups=decisions.cortex,resources=schedulingdecisions,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=decisions.cortex,resources=schedulingdecisions/status,verbs=get

func (r *SchedulingDecisionTTLController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("ttl-controller")

	// Fetch the decision object
	var decision decisionsv1alpha1.SchedulingDecision
	if err := r.Get(ctx, req.NamespacedName, &decision); err != nil {
		// Resource was deleted or doesn't exist - nothing to clean up
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.processResourceForTTL(ctx, &decision, log)
}

func (r *SchedulingDecisionTTLController) getTTL() time.Duration {
	if r.Conf.TTLAfterDecisionSeconds > 0 {
		return time.Duration(r.Conf.TTLAfterDecisionSeconds) * time.Second
	}
	return time.Duration(DefaultTTLAfterDecisionSeconds) * time.Second
}

// processResourceForTTL handles the common TTL logic for a single resource
func (r *SchedulingDecisionTTLController) processResourceForTTL(ctx context.Context, decision *decisionsv1alpha1.SchedulingDecision, log logr.Logger) (ctrl.Result, error) {
	// Calculate age based on last decision's RequestedAt timestamp
	var referenceTime time.Time
	if len(decision.Spec.Decisions) > 0 {
		// Use the last decision's RequestedAt timestamp
		lastDecision := decision.Spec.Decisions[len(decision.Spec.Decisions)-1]
		referenceTime = lastDecision.RequestedAt.Time
	} else {
		// Fallback to creation timestamp if no decisions exist
		referenceTime = decision.CreationTimestamp.Time
	}

	age := time.Since(referenceTime)
	ttl := r.getTTL()

	if age >= ttl {
		// TTL has expired - delete the resource
		log.Info("Deleting expired SchedulingDecision",
			"name", decision.Name,
			"age", age.String(),
			"ttl", ttl.String())

		if err := r.Delete(ctx, decision); err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to delete expired SchedulingDecision", "name", decision.Name)
				return ctrl.Result{}, err
			}
			log.V(1).Info("SchedulingDecision was already deleted", "name", decision.Name)
		}

		return ctrl.Result{}, nil
	}

	remainingTime := ttl - age
	log.V(1).Info("Scheduling SchedulingDecision for future deletion",
		"name", decision.Name,
		"remainingTime", remainingTime.String())

	return ctrl.Result{RequeueAfter: remainingTime}, nil
}

// reconcileAllResourcesOnStartup processes all existing SchedulingDecision resources
// to check for expired ones that should be cleaned up after controller restart
func (r *SchedulingDecisionTTLController) reconcileAllResourcesOnStartup(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("ttl-startup-reconciler")

	var resources decisionsv1alpha1.SchedulingDecisionList
	if err := r.List(ctx, &resources); err != nil {
		log.Error(err, "Failed to list SchedulingDecision resources during startup reconciliation")
		return
	}

	log.Info("Processing existing resources for TTL cleanup", "resourceCount", len(resources.Items))

	processedCount := 0
	expiredCount := 0

	for _, resource := range resources.Items {
		// Use the shared TTL processing logic
		result, err := r.processResourceForTTL(ctx, &resource, log)
		if err != nil {
			log.Error(err, "Failed to process resource during startup reconciliation", "name", resource.Name)
		} else if result.RequeueAfter == 0 {
			// Resource was deleted (no requeue means it was expired and deleted)
			expiredCount++
		}
		processedCount++
	}

	log.Info("Startup TTL reconciliation completed",
		"processedResources", processedCount,
		"expiredResources", expiredCount)
}

func (r *SchedulingDecisionTTLController) SetupWithManager(mgr ctrl.Manager) error {
	log := mgr.GetLogger().WithName("ttl-controller")

	// Log the TTL configuration on startup
	ttl := r.getTTL()
	seconds := r.Conf.TTLAfterDecisionSeconds
	if seconds == 0 {
		seconds = DefaultTTLAfterDecisionSeconds
	}
	log.Info("TTL Controller configured", "ttlAfterDecisionSeconds", seconds, "ttlAfterDecision", ttl.String())

	// Add the startup reconciler as a runnable
	if err := mgr.Add(&TTLStartupReconciler{ttlController: r}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&decisionsv1alpha1.SchedulingDecision{}).
		Named("schedulingdecision-ttl").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		WithEventFilter(
			// Watch for spec changes (when decisions are added/modified)
			predicate.GenerationChangedPredicate{},
		).
		Complete(r)
}

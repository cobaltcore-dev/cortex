// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	decisionsv1alpha1 "github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

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

		if err := r.Delete(ctx, &decision); err != nil {
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

func (r *SchedulingDecisionTTLController) getTTL() time.Duration {
	if r.Conf.TTLAfterDecision > 0 {
		return r.Conf.TTLAfterDecision
	}
	return DefaultTTLAfterDecision
}

func (r *SchedulingDecisionTTLController) SetupWithManager(mgr ctrl.Manager) error {
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

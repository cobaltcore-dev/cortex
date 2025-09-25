// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
	decisionsv1alpha1 "github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

// SchedulingDecisionReconciler reconciles a SchedulingDecision object
type SchedulingDecisionReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the decisions.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
}

// +kubebuilder:rbac:groups=decisions.cortex,resources=schedulingdecisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decisions.cortex,resources=schedulingdecisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=decisions.cortex,resources=schedulingdecisions/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SchedulingDecisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)
	// Fetch the decision object.
	var res v1alpha1.SchedulingDecision
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Can happen when the resource was just deleted.
		return ctrl.Result{}, err
	}

	res.Status.Description = "...."
	if err := r.Status().Update(ctx, &res); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil // No need to requeue.
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulingDecisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decisionsv1alpha1.SchedulingDecision{}).
		Named("schedulingdecision").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Default
		}).
		Complete(r)
}

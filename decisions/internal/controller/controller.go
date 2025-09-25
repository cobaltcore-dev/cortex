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

// ComputeDecisionReconciler reconciles a ComputeDecision object
type ComputeDecisionReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the decisions.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
}

// +kubebuilder:rbac:groups=decisions.cortex,resources=computedecisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decisions.cortex,resources=computedecisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=decisions.cortex,resources=computedecisions/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComputeDecisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)
	// Fetch the decision object.
	var res v1alpha1.ComputeDecision
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Can happen when the resource was just deleted.
		return ctrl.Result{}, err
	}

	// TODO: Reconciliation logic.

	return ctrl.Result{}, nil // No need to requeue.
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComputeDecisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decisionsv1alpha1.ComputeDecision{}).
		Named("computedecision").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Default
		}).
		Complete(r)
}

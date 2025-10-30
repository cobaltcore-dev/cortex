// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"sort"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The explanation controller populates two fields of the decision status.
//
// First, it reconstructs the history of each decision. It will look for
// previous decisions for the same resource (based on ResourceID) and provide
// them through the decision history field.
//
// Second, it will use the available context for a decision to generate a
// human-readable explanation of why the decision was made the way it was.
// This explanation is intended to help operators understand the reasoning
// behind scheduling decisions.
type Controller struct {
	// The kubernetes client to use for processing decisions.
	client.Client
	// The controller will scope to objects using this operator name.
	// This allows multiple operators to coexist in the same cluster without
	// interfering with each other's decisions.
	OperatorName string
	// If the field indexing should be skipped (useful for testing).
	SkipIndexFields bool
}

// Check if a decision should be processed by this controller.
func (c *Controller) shouldReconcileDecision(decision *v1alpha1.Decision) bool {
	// Ignore decisions not created by this operator.
	if decision.Spec.Operator != c.OperatorName {
		return false
	}
	// Ignore decisions that already have an explanation.
	if decision.Status.Explanation != "" {
		return false
	}
	// Only handle nova decisions.
	return decision.Spec.Type == v1alpha1.DecisionTypeNovaServer
}

// This loop will be called by the controller-runtime for each decision
// resource that needs to be reconciled.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		log.Error(err, "failed to get decision", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Reconcile the history.
	if err := c.reconcileHistory(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	// Reconcile the explanation.
	if err := c.reconcileExplanation(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("successfully reconciled decision explanation", "name", req.NamespacedName)
	return ctrl.Result{}, nil
}

// Process the history for the given decision.
func (c *Controller) reconcileHistory(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	// Get all previous decisions for the same ResourceID.
	var previousDecisions v1alpha1.DecisionList
	if c.SkipIndexFields {
		// When field indexing is skipped, list all decisions and filter manually
		if err := c.List(ctx, &previousDecisions); err != nil {
			log.Error(err, "failed to list all decisions", "resourceID", decision.Spec.ResourceID)
			return err
		}
		// Filter to only decisions with matching ResourceID
		var filteredDecisions []v1alpha1.Decision
		for _, prevDecision := range previousDecisions.Items {
			if prevDecision.Spec.ResourceID == decision.Spec.ResourceID {
				filteredDecisions = append(filteredDecisions, prevDecision)
			}
		}
		previousDecisions.Items = filteredDecisions
	} else {
		// Use field indexing for efficient lookup
		if err := c.List(ctx, &previousDecisions, client.MatchingFields{"spec.resourceID": decision.Spec.ResourceID}); err != nil {
			log.Error(err, "failed to list previous decisions", "resourceID", decision.Spec.ResourceID)
			return err
		}
	}
	history := []corev1.ObjectReference{} // Not var-init so we see the empty slice.
	// Make sure the resulting history will be in chronological order.
	sort.Slice(previousDecisions.Items, func(i, j int) bool {
		t1 := previousDecisions.Items[i].CreationTimestamp
		t2 := previousDecisions.Items[j].CreationTimestamp
		return t1.Before(&t2)
	})
	for _, prevDecision := range previousDecisions.Items {
		// Skip the current decision.
		if prevDecision.Name == decision.Name && prevDecision.Namespace == decision.Namespace {
			continue
		}
		// Skip decisions that were made after the current one.
		if prevDecision.CreationTimestamp.After(decision.CreationTimestamp.Time) {
			continue
		}
		history = append(history, corev1.ObjectReference{
			Kind:      "Decision",
			Namespace: prevDecision.Namespace,
			Name:      prevDecision.Name,
			UID:       prevDecision.UID,
		})
	}
	decision.Status.History = &history
	precedence := len(history)
	decision.Status.Precedence = &precedence
	if err := c.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status with history", "name", decision.Name)
		return err
	}
	log.Info("successfully reconciled decision history", "name", decision.Name)
	return nil
}

// Process the explanation for the given decision.
func (c *Controller) reconcileExplanation(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	explainer := &Explainer{Client: c.Client}
	explanationText, err := explainer.Explain(ctx, decision)
	if err != nil {
		log.Error(err, "failed to explain decision", "name", decision.Name)
		return err
	}
	decision.Status.Explanation = explanationText
	if err := c.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status with explanation", "name", decision.Name)
		return err
	}
	log.Info("successfully reconciled decision explanation", "name", decision.Name)
	return nil
}

// This function will be called when the manager starts up. Must block.
func (c *Controller) StartupCallback(ctx context.Context) error {
	// Reprocess all existing decisions that need an explanation.
	var decisions v1alpha1.DecisionList
	if err := c.List(ctx, &decisions); err != nil {
		return err
	}
	for _, decision := range decisions.Items {
		if !c.shouldReconcileDecision(&decision) {
			continue
		}
		if _, err := c.Reconcile(ctx, ctrl.Request{
			NamespacedName: client.ObjectKey{
				Namespace: decision.Namespace,
				Name:      decision.Name,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// This function sets up the controller with the provided manager.
func (c *Controller) SetupWithManager(mgr manager.Manager) error {
	// Index the decisions by ResourceID for efficient lookup.
	if !c.SkipIndexFields {
		if err := mgr.GetFieldIndexer().IndexField(
			context.Background(),
			&v1alpha1.Decision{},
			"spec.resourceID",
			func(obj client.Object) []string {
				decision := obj.(*v1alpha1.Decision)
				return []string{decision.Spec.ResourceID}
			},
		); err != nil {
			return err
		}
	}
	if err := mgr.Add(manager.RunnableFunc(c.StartupCallback)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("explanation-controller").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				return c.shouldReconcileDecision(decision)
			})),
		).
		Complete(c)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TriggerReconciler watches datasource and knowledge changes to trigger
// reconciliation of dependent knowledge resources based on recency requirements.
type TriggerReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use.
	Scheme *runtime.Scheme
	// Config for the reconciler.
	Conf conf.Config
}

// Reconcile handles changes to datasource and knowledge resources and triggers
// reconciliation of dependent knowledge resources.
func (r *TriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Try to get the changed resource as a datasource first
	datasource := &v1alpha1.Datasource{}
	isDatasource := true
	if err := r.Get(ctx, req.NamespacedName, datasource); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		isDatasource = false
	}

	// If not a datasource, try to get it as a knowledge resource
	var changedResource client.Object
	if isDatasource {
		changedResource = datasource
	} else {
		knowledge := &v1alpha1.Knowledge{}
		if err := r.Get(ctx, req.NamespacedName, knowledge); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		changedResource = knowledge
	}
	log.Info(
		"trigger: resource changed, finding dependents",
		"resource", changedResource.GetName(),
		"type", getResourceType(changedResource),
	)

	// Find all knowledge resources that depend on the changed resource
	dependentKnowledge, err := r.findDependentKnowledge(ctx, changedResource)
	if err != nil {
		log.Error(err, "failed to find dependent knowledge")
		return ctrl.Result{}, err
	}
	if len(dependentKnowledge) == 0 {
		log.Info("trigger: no dependent knowledge found")
		return ctrl.Result{}, nil
	}

	log.Info("trigger: found dependent knowledge", "count", len(dependentKnowledge))
	// Process each dependent knowledge resource
	for _, knowledge := range dependentKnowledge {
		if err := r.triggerKnowledgeReconciliation(ctx, knowledge); err != nil {
			log.Error(err, "failed to trigger knowledge reconciliation", "knowledge", knowledge.Name)
			// Continue with other knowledge resources even if one fails
		}
	}
	return ctrl.Result{}, nil
}

// findDependentKnowledge finds all knowledge resources that depend on the given resource
func (r *TriggerReconciler) findDependentKnowledge(ctx context.Context, changedResource client.Object) ([]v1alpha1.Knowledge, error) {
	log := logf.FromContext(ctx)

	knowledgeList := &v1alpha1.KnowledgeList{}
	if err := r.List(ctx, knowledgeList); err != nil {
		return nil, err
	}

	var dependents []v1alpha1.Knowledge
	changedResourceName := changedResource.GetName()
	changedResourceType := getResourceType(changedResource)
	for _, knowledge := range knowledgeList.Items {
		// Only process knowledge for our operator
		if knowledge.Spec.Operator != r.Conf.Operator {
			continue
		}

		isDependentOnChanged := false
		if changedResourceType == "Datasource" {
			for _, dsRef := range knowledge.Spec.Dependencies.Datasources {
				if dsRef.Name == changedResourceName {
					isDependentOnChanged = true
					break
				}
			}
		}
		if changedResourceType == "Knowledge" {
			for _, kRef := range knowledge.Spec.Dependencies.Knowledges {
				if kRef.Name == changedResourceName {
					isDependentOnChanged = true
					break
				}
			}
		}
		if isDependentOnChanged {
			log.Info(
				"trigger: found dependent knowledge",
				"knowledge", knowledge.Name,
				"dependsOn", changedResourceName,
			)
			dependents = append(dependents, knowledge)
		}
	}
	return dependents, nil
}

// Determine when to trigger reconciliation based on recency.
func (r *TriggerReconciler) triggerKnowledgeReconciliation(ctx context.Context, knowledge v1alpha1.Knowledge) error {
	log := logf.FromContext(ctx)

	lastExtracted := knowledge.Status.LastExtracted.Time
	recency := knowledge.Spec.Recency.Duration
	now := time.Now()

	// Calculate when the next reconciliation should happen based on recency
	nextReconciliationTime := lastExtracted.Add(recency)
	if nextReconciliationTime.Before(now) || nextReconciliationTime.Equal(now) {
		// Recency threshold already reached - trigger immediate reconciliation
		log.Info(
			"trigger: immediate reconciliation needed",
			"knowledge", knowledge.Name,
			"lastExtracted", lastExtracted,
			"recency", recency,
			"nextDue", nextReconciliationTime,
		)
		return r.enqueueKnowledgeReconciliation(ctx, knowledge, time.Duration(0))
	} else {
		// Schedule reconciliation for when recency threshold will be reached
		delay := time.Until(nextReconciliationTime)
		log.Info(
			"trigger: scheduling future reconciliation",
			"knowledge", knowledge.Name,
			"delay", delay,
			"scheduledFor", nextReconciliationTime,
		)
		return r.enqueueKnowledgeReconciliation(ctx, knowledge, delay)
	}
}

// Enqueue a knowledge resource for reconciliation.
func (r *TriggerReconciler) enqueueKnowledgeReconciliation(ctx context.Context, knowledge v1alpha1.Knowledge, delay time.Duration) error {
	log := logf.FromContext(ctx)

	// The controller-runtime framework will automatically handle the delayed reconciliation
	// We update the knowledge annotation to trigger reconciliation by the main KnowledgeReconciler
	if knowledge.Annotations == nil {
		knowledge.Annotations = make(map[string]string)
	}

	// Add a trigger annotation with current timestamp to force reconciliation
	knowledge.Annotations["cortex.knowledge/trigger-reconciliation"] = time.Now().Format(time.RFC3339)
	if err := r.Update(ctx, &knowledge); err != nil {
		log.Error(err, "failed to update knowledge to trigger reconciliation", "knowledge", knowledge.Name)
		return err
	}
	log.Info(
		"trigger: enqueued knowledge for reconciliation",
		"knowledge", knowledge.Name,
		"delay", delay,
	)
	return nil
}

// Helper returning the resource type as a string.
func getResourceType(obj client.Object) string {
	switch obj.(type) {
	case *v1alpha1.Datasource:
		return "Datasource"
	case *v1alpha1.Knowledge:
		return "Knowledge"
	default:
		return "Unknown"
	}
}

// Map datasource changes to knowledge reconcile requests.
func (r *TriggerReconciler) mapDatasourceToKnowledge(ctx context.Context, obj client.Object) []reconcile.Request {
	datasource, ok := obj.(*v1alpha1.Datasource)
	if !ok {
		return nil
	}
	// Only process datasources for our operator
	if datasource.Spec.Operator != r.Conf.Operator {
		return nil
	}
	// Return a request that will trigger our reconciler to find dependents
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      datasource.Name,
				Namespace: datasource.Namespace,
			},
		},
	}
}

// Map knowledge changes to other knowledge reconcile requests.
func (r *TriggerReconciler) mapKnowledgeToKnowledge(ctx context.Context, obj client.Object) []reconcile.Request {
	knowledge, ok := obj.(*v1alpha1.Knowledge)
	if !ok {
		return nil
	}
	// Only process knowledge for our operator
	if knowledge.Spec.Operator != r.Conf.Operator {
		return nil
	}
	// Return a request that will trigger our reconciler to find dependents
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      knowledge.Name,
				Namespace: knowledge.Namespace,
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *TriggerReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-knowledge-trigger").
		// Watch datasource changes and map them to trigger reconciliation
		Watches(
			&v1alpha1.Datasource{},
			handler.EnqueueRequestsFromMapFunc(r.mapDatasourceToKnowledge),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				ds := obj.(*v1alpha1.Datasource)
				return ds.Spec.Operator == r.Conf.Operator
			})),
		).
		// Watch knowledge changes and map them to trigger reconciliation
		Watches(
			&v1alpha1.Knowledge{},
			handler.EnqueueRequestsFromMapFunc(r.mapKnowledgeToKnowledge),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				k := obj.(*v1alpha1.Knowledge)
				return k.Spec.Operator == r.Conf.Operator
			})),
		).
		Complete(r)
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"sort"

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

	// Validate that there is at least one host in the input
	if len(res.Spec.Input) == 0 {
		res.Status.State = v1alpha1.SchedulingDecisionStateError
		res.Status.Error = "No hosts provided in input"
	} else {
		// Validate that all hosts in pipeline outputs exist in input
		for _, output := range res.Spec.Pipeline.Outputs {
			for hostName := range output.Activations {
				if _, exists := res.Spec.Input[hostName]; !exists {
					res.Status.State = v1alpha1.SchedulingDecisionStateError
					res.Status.Error = "Host '" + hostName + "' in pipeline output not found in input"
					if err := r.Status().Update(ctx, &res); err != nil {
						return ctrl.Result{}, err
					}
					return ctrl.Result{}, nil
				}
			}
		}

		// Calculate final scores for all hosts
		finalScores := make(map[string]float64)
		deletedHosts := make(map[string][]string)

		// Start with input values as initial scores
		for hostName, inputValue := range res.Spec.Input {
			finalScores[hostName] = inputValue
		}

		// Process each pipeline step sequentially
		for _, output := range res.Spec.Pipeline.Outputs {
			// Check which hosts will be deleted in this step
			for hostName := range finalScores {
				if _, exists := output.Activations[hostName]; !exists {
					// Host not in this step's activations - will be deleted
					deletedHosts[hostName] = append(deletedHosts[hostName], output.Step)
				}
			}

			// Apply activations and remove hosts not in this step
			for hostName := range finalScores {
				if activation, exists := output.Activations[hostName]; exists {
					// Add activation to current score
					finalScores[hostName] = finalScores[hostName] + activation
				} else {
					// Host not in this step - remove it
					delete(finalScores, hostName)
				}
			}
		}

		res.Status.State = v1alpha1.SchedulingDecisionStateResolved
		res.Status.Error = ""

		// Sort finalScores by score (highest to lowest) and generate enhanced description
		orderedScores, description := r.generateOrderedScoresAndDescription(finalScores, len(res.Spec.Input))

		res.Status.FinalScores = orderedScores
		res.Status.DeletedHosts = deletedHosts
		res.Status.Description = description
	}

	if err := r.Status().Update(ctx, &res); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil // No need to requeue.
}

// generateOrderedScoresAndDescription sorts final scores by value (highest to lowest)
// and generates a brief description with highest host, certainty, and host count
func (r *SchedulingDecisionReconciler) generateOrderedScoresAndDescription(finalScores map[string]float64, totalInputHosts int) (map[string]float64, string) {
	if len(finalScores) == 0 {
		return finalScores, fmt.Sprintf("No hosts remaining after filtering, %d hosts evaluated", totalInputHosts)
	}

	// Create a slice of host-score pairs for sorting
	type hostScore struct {
		host  string
		score float64
	}

	var sortedHosts []hostScore
	for host, score := range finalScores {
		sortedHosts = append(sortedHosts, hostScore{host: host, score: score})
	}

	// Sort by score (highest to lowest)
	sort.Slice(sortedHosts, func(i, j int) bool {
		return sortedHosts[i].score > sortedHosts[j].score
	})

	// Create ordered map (Go maps maintain insertion order as of Go 1.8+)
	orderedScores := make(map[string]float64)
	for _, hs := range sortedHosts {
		orderedScores[hs.host] = hs.score
	}

	// Generate description
	var description string
	if len(sortedHosts) == 1 {
		description = fmt.Sprintf("Selected: %s (score: %.2f), certainty: perfect, %d hosts evaluated",
			sortedHosts[0].host, sortedHosts[0].score, totalInputHosts)
	} else {
		// Calculate certainty based on gap between 1st and 2nd place
		gap := sortedHosts[0].score - sortedHosts[1].score
		var certainty string
		if gap >= 0.5 {
			certainty = "high"
		} else if gap >= 0.2 {
			certainty = "medium"
		} else {
			certainty = "low"
		}

		description = fmt.Sprintf("Selected: %s (score: %.2f), certainty: %s (gap: %.2f), %d hosts evaluated",
			sortedHosts[0].host, sortedHosts[0].score, certainty, gap, totalInputHosts)
	}

	return orderedScores, description
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

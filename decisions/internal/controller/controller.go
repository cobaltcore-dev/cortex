// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
	decisionsv1alpha1 "github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

const (
	// MinScoreValue represents the minimum possible score value
	MinScoreValue = -999999

	// String format templates for descriptions
	selectedPerfectFmt   = "Selected: %s (score: %.2f), certainty: perfect, %d hosts evaluated."
	selectedCertaintyFmt = "Selected: %s (score: %.2f), certainty: %s (gap: %.2f), %d hosts evaluated."
	noHostsRemainingFmt  = "No hosts remaining after filtering, %d hosts evaluated"
	inputConfirmedFmt    = " Input choice confirmed: %s (%.2f→%.2f, remained #1)."
	inputFilteredFmt     = " Input favored %s (score: %.2f, now filtered), final winner was #%d in input (%.2f→%.2f)."
	inputDemotedFmt      = " Input favored %s (score: %.2f, now #%d with %.2f), final winner was #%d in input (%.2f→%.2f)."
)

// certaintyLevel represents a threshold and its corresponding certainty level
type certaintyLevel struct {
	threshold float64
	level     string
}

// certaintyLevels maps score gaps to certainty levels (ordered from highest to lowest threshold)
var certaintyLevels = []certaintyLevel{
	{0.5, "high"},
	{0.2, "medium"},
	{0.0, "low"},
}

// getCertaintyLevel returns the certainty level for a given score gap
func getCertaintyLevel(gap float64) string {
	for _, cl := range certaintyLevels {
		if gap >= cl.threshold {
			return cl.level
		}
	}
	return "low" // fallback
}

// hostScore represents a host-score pair for sorting operations
type hostScore struct {
	host  string
	score float64
}

// mapToSortedHostScores converts a score map to sorted hostScore slice (highest to lowest)
func mapToSortedHostScores(scores map[string]float64) []hostScore {
	sorted := make([]hostScore, 0, len(scores))
	for host, score := range scores {
		sorted = append(sorted, hostScore{host: host, score: score})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})
	return sorted
}

// findHostPosition returns the 1-based position of a host in sorted hosts slice
func findHostPosition(hosts []hostScore, targetHost string) int {
	for i, hs := range hosts {
		if hs.host == targetHost {
			return i + 1 // 1-based position
		}
	}
	return -1
}

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

	// If the decision is already resolved or in error state, do nothing.
	if res.Status.State == v1alpha1.SchedulingDecisionStateResolved || res.Status.State == v1alpha1.SchedulingDecisionStateError {
		return ctrl.Result{}, nil
	}

	// Validate we have at least one decision
	if len(res.Spec.Decisions) == 0 {
		if err := r.setErrorState(ctx, &res, fmt.Errorf("No decisions provided in spec")); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Process each decision individually
	results := make([]v1alpha1.SchedulingDecisionResult, 0, len(res.Spec.Decisions))

	for _, decision := range res.Spec.Decisions {
		// Validate input has at least one host for this decision
		if err := r.validateInput(decision.Input); err != nil {
			if err := r.setErrorState(ctx, &res, fmt.Errorf("Decision %s: %v", decision.ID, err)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Validate that all hosts in pipeline outputs exist in input for this decision
		if err := r.validatePipelineHosts(decision.Input, decision.Pipeline.Outputs); err != nil {
			if err := r.setErrorState(ctx, &res, fmt.Errorf("Decision %s: %v", decision.ID, err)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Calculate final scores with full pipeline for this decision
		finalScores, deletedHosts := r.calculateScores(decision.Input, decision.Pipeline.Outputs)

		// Calculate step-by-step impact for the winner for this decision
		stepImpacts := r.calculateStepImpacts(decision.Input, decision.Pipeline.Outputs, finalScores)

		// Find minimal critical path for this decision
		criticalSteps, criticalStepCount := r.findCriticalSteps(decision.Input, decision.Pipeline.Outputs, finalScores)

		// Sort finalScores by score (highest to lowest) and generate enhanced description for this decision
		orderedScores, description := r.generateOrderedScoresAndDescription(finalScores, decision.Input, criticalSteps, criticalStepCount, len(decision.Pipeline.Outputs), stepImpacts)

		// Create result for this decision
		result := v1alpha1.SchedulingDecisionResult{
			ID:           decision.ID,
			Description:  description,
			FinalScores:  orderedScores,
			DeletedHosts: deletedHosts,
		}
		results = append(results, result)
	}

	// Update status with all results
	res.Status.State = v1alpha1.SchedulingDecisionStateResolved
	res.Status.Error = ""
	res.Status.DecisionCount = len(res.Spec.Decisions)
	res.Status.Results = results

	if err := r.Status().Update(ctx, &res); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil // No need to requeue.
}

// validateInput checks if the input has at least one host
func (r *SchedulingDecisionReconciler) validateInput(input map[string]float64) error {
	if len(input) == 0 {
		return fmt.Errorf("No hosts provided in input")
	}
	return nil
}

// validatePipelineHosts checks if all hosts in pipeline outputs exist in input
func (r *SchedulingDecisionReconciler) validatePipelineHosts(input map[string]float64, outputs []v1alpha1.SchedulingDecisionPipelineOutputSpec) error {
	for _, output := range outputs {
		for hostName := range output.Activations {
			if _, exists := input[hostName]; !exists {
				return fmt.Errorf("Host '%s' in pipeline output not found in input", hostName)
			}
		}
	}
	return nil
}

// setErrorState sets the error state and updates the resource status
func (r *SchedulingDecisionReconciler) setErrorState(ctx context.Context, res *v1alpha1.SchedulingDecision, err error) error {
	res.Status.State = v1alpha1.SchedulingDecisionStateError
	res.Status.Error = err.Error()
	return r.Status().Update(ctx, res)
}

// findWinner returns the host with the highest score and the score value
func findWinner(scores map[string]float64) (string, float64) {
	if len(scores) == 0 {
		return "", MinScoreValue
	}

	winner := ""
	maxScore := float64(MinScoreValue)
	for host, score := range scores {
		if score > maxScore {
			maxScore = score
			winner = host
		}
	}
	return winner, maxScore
}

// calculateScores processes pipeline outputs and returns final scores and deleted hosts
func (r *SchedulingDecisionReconciler) calculateScores(input map[string]float64, outputs []v1alpha1.SchedulingDecisionPipelineOutputSpec) (map[string]float64, map[string][]string) {
	finalScores := make(map[string]float64, len(input))
	deletedHosts := make(map[string][]string)

	// Start with input values as initial scores
	for hostName, inputValue := range input {
		finalScores[hostName] = inputValue
	}

	// Process each pipeline step sequentially
	for _, output := range outputs {
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

	return finalScores, deletedHosts
}

// findCriticalSteps identifies which pipeline steps are essential for the final decision
// using backward elimination approach
func (r *SchedulingDecisionReconciler) findCriticalSteps(input map[string]float64, outputs []v1alpha1.SchedulingDecisionPipelineOutputSpec, baselineFinalScores map[string]float64) ([]string, int) {
	if len(outputs) == 0 {
		return []string{}, 0
	}

	// Get baseline winner
	baselineWinner, _ := findWinner(baselineFinalScores)
	if baselineWinner == "" {
		return []string{}, 0
	}

	criticalSteps := make([]string, 0)

	// Try removing each step one by one
	for i, stepToRemove := range outputs {
		// Create pipeline without this step using slice operations
		reducedOutputs := make([]v1alpha1.SchedulingDecisionPipelineOutputSpec, 0, len(outputs)-1)
		reducedOutputs = append(reducedOutputs, outputs[:i]...)
		reducedOutputs = append(reducedOutputs, outputs[i+1:]...)

		// Calculate scores without this step
		reducedFinalScores, _ := r.calculateScores(input, reducedOutputs)

		// Find winner without this step
		reducedWinner, _ := findWinner(reducedFinalScores)

		// If removing this step changes the winner, it's critical
		if reducedWinner != baselineWinner {
			criticalSteps = append(criticalSteps, stepToRemove.Step)
		}
	}

	return criticalSteps, len(criticalSteps)
}

// StepImpact represents the impact of a single pipeline step on the winning host
type StepImpact struct {
	Step               string
	ScoreBefore        float64
	ScoreAfter         float64
	ScoreDelta         float64
	CompetitorsRemoved int
	PromotedToFirst    bool
}

// calculateStepImpacts tracks how each pipeline step affects the final winner
func (r *SchedulingDecisionReconciler) calculateStepImpacts(input map[string]float64, outputs []v1alpha1.SchedulingDecisionPipelineOutputSpec, finalScores map[string]float64) []StepImpact {
	if len(finalScores) == 0 || len(outputs) == 0 {
		return []StepImpact{}
	}

	// Find the final winner
	finalWinner, _ := findWinner(finalScores)
	if finalWinner == "" {
		return []StepImpact{}
	}

	stepImpacts := make([]StepImpact, 0, len(outputs))
	currentScores := make(map[string]float64)

	// Start with input values as initial scores
	for hostName, inputValue := range input {
		currentScores[hostName] = inputValue
	}

	// Track score before first step
	scoreBefore := currentScores[finalWinner]

	// Process each pipeline step and track the winner's evolution
	for _, output := range outputs {
		// Count how many competitors will be removed in this step
		competitorsRemoved := 0
		for hostName := range currentScores {
			if hostName != finalWinner {
				if _, exists := output.Activations[hostName]; !exists {
					competitorsRemoved++
				}
			}
		}

		// Check if winner was #1 before this step
		wasFirst := true
		winnerScoreBefore := currentScores[finalWinner]
		for host, score := range currentScores {
			if host != finalWinner && score > winnerScoreBefore {
				wasFirst = false
				break
			}
		}

		// Apply activations and remove hosts not in this step
		newScores := make(map[string]float64)
		for hostName, score := range currentScores {
			if activation, exists := output.Activations[hostName]; exists {
				newScores[hostName] = score + activation
			}
			// Hosts not in activations are removed (don't copy to newScores)
		}

		// Get winner's score after this step
		scoreAfter := newScores[finalWinner]

		// Check if winner became #1 after this step
		isFirstAfter := true
		for host, score := range newScores {
			if host != finalWinner && score > scoreAfter {
				isFirstAfter = false
				break
			}
		}

		promotedToFirst := !wasFirst && isFirstAfter

		stepImpacts = append(stepImpacts, StepImpact{
			Step:               output.Step,
			ScoreBefore:        scoreBefore,
			ScoreAfter:         scoreAfter,
			ScoreDelta:         scoreAfter - scoreBefore,
			CompetitorsRemoved: competitorsRemoved,
			PromotedToFirst:    promotedToFirst,
		})

		// Update for next iteration
		currentScores = newScores
		scoreBefore = scoreAfter
	}

	return stepImpacts
}

// generateOrderedScoresAndDescription sorts final scores by value (highest to lowest)
// and generates a brief description with highest host, certainty, host count, input comparison, step impacts, and critical path
func (r *SchedulingDecisionReconciler) generateOrderedScoresAndDescription(finalScores map[string]float64, inputScores map[string]float64, criticalSteps []string, criticalStepCount int, totalSteps int, stepImpacts []StepImpact) (map[string]float64, string) {
	totalInputHosts := len(inputScores)
	if len(finalScores) == 0 {
		return finalScores, fmt.Sprintf(noHostsRemainingFmt, totalInputHosts)
	}

	// Sort final scores by value (highest to lowest)
	sortedHosts := mapToSortedHostScores(finalScores)

	// Create ordered map (Go maps maintain insertion order as of Go 1.8+)
	orderedScores := make(map[string]float64)
	for _, hs := range sortedHosts {
		orderedScores[hs.host] = hs.score
	}

	// Sort input scores to determine input-based ranking
	sortedInputHosts := mapToSortedHostScores(inputScores)

	// Find positions and generate comparison
	finalWinner := sortedHosts[0].host
	inputWinner := sortedInputHosts[0].host
	finalWinnerInputScore := inputScores[finalWinner]

	// Find final winner's position in input ranking
	finalWinnerInputPosition := findHostPosition(sortedInputHosts, finalWinner)

	// Generate main description
	var description string
	if len(sortedHosts) == 1 {
		description = fmt.Sprintf(selectedPerfectFmt, sortedHosts[0].host, sortedHosts[0].score, totalInputHosts)
	} else {
		// Calculate certainty based on gap between 1st and 2nd place
		gap := sortedHosts[0].score - sortedHosts[1].score
		certainty := getCertaintyLevel(gap)
		description = fmt.Sprintf(selectedCertaintyFmt, sortedHosts[0].host, sortedHosts[0].score, certainty, gap, totalInputHosts)
	}

	// Add input vs. final comparison
	var comparison string
	if inputWinner == finalWinner {
		// Input choice confirmed
		comparison = fmt.Sprintf(inputConfirmedFmt, finalWinner, finalWinnerInputScore, sortedHosts[0].score)
	} else {
		// Input winner different from final winner
		inputWinnerScore := sortedInputHosts[0].score

		// Check if input winner was filtered out
		_, inputWinnerSurvived := finalScores[inputWinner]
		if !inputWinnerSurvived {
			comparison = fmt.Sprintf(inputFilteredFmt, inputWinner, inputWinnerScore, finalWinnerInputPosition, finalWinnerInputScore, sortedHosts[0].score)
		} else {
			// Find input winner's position in final ranking
			inputWinnerFinalPosition := findHostPosition(sortedHosts, inputWinner)
			comparison = fmt.Sprintf(inputDemotedFmt, inputWinner, inputWinnerScore, inputWinnerFinalPosition, finalScores[inputWinner],
				finalWinnerInputPosition, finalWinnerInputScore, sortedHosts[0].score)
		}
	}

	// Add step impact analysis for the winner using multi-line format
	var stepImpactInfo string
	if len(stepImpacts) > 0 {
		stepImpactInfo = r.formatStepImpactsMultiLine(stepImpacts)
	}

	// Add critical path information
	var criticalPath string
	if totalSteps > 0 {
		if criticalStepCount == 0 {
			criticalPath = fmt.Sprintf(" Decision driven by input only (all %d steps are non-critical).", totalSteps)
		} else if criticalStepCount == totalSteps {
			criticalPath = fmt.Sprintf(" Decision requires all %d pipeline steps.", totalSteps)
		} else {
			if criticalStepCount == 1 {
				criticalPath = fmt.Sprintf(" Decision driven by 1/%d pipeline step: %s.", totalSteps, criticalSteps[0])
			} else {
				// Join critical steps with proper separators
				var stepList string
				if len(criticalSteps) == 2 {
					stepList = strings.Join(criticalSteps, " and ")
				} else {
					// For 3+ steps: "step1, step2, and step3"
					lastStep := criticalSteps[len(criticalSteps)-1]
					otherSteps := criticalSteps[:len(criticalSteps)-1]
					stepList = strings.Join(otherSteps, ", ") + " and " + lastStep
				}
				criticalPath = fmt.Sprintf(" Decision driven by %d/%d pipeline steps: %s.", criticalStepCount, totalSteps, stepList)
			}
		}
	}

	description += comparison + criticalPath + stepImpactInfo
	return orderedScores, description
}

// formatImpactValue formats a single step impact value
func formatImpactValue(impact StepImpact) string {
	if impact.PromotedToFirst {
		return fmt.Sprintf("%+.2f→#1", impact.ScoreDelta)
	}
	if impact.ScoreDelta != 0 {
		return fmt.Sprintf("%+.2f", impact.ScoreDelta)
	}
	if impact.CompetitorsRemoved > 0 {
		return fmt.Sprintf("+0.00 (removed %d)", impact.CompetitorsRemoved)
	}
	return "+0.00"
}

// formatStepImpactsMultiLine formats step impacts in a simple delta-ordered format
// without confusing terminology, ordered by absolute impact magnitude
func (r *SchedulingDecisionReconciler) formatStepImpactsMultiLine(stepImpacts []StepImpact) string {
	if len(stepImpacts) == 0 {
		return ""
	}

	// Sort by absolute delta impact (highest first), with promotions taking priority for ties
	sort.Slice(stepImpacts, func(i, j int) bool {
		absI, absJ := math.Abs(stepImpacts[i].ScoreDelta), math.Abs(stepImpacts[j].ScoreDelta)
		if absI != absJ {
			return absI > absJ
		}
		if stepImpacts[i].PromotedToFirst != stepImpacts[j].PromotedToFirst {
			return stepImpacts[i].PromotedToFirst
		}
		return stepImpacts[i].Step < stepImpacts[j].Step
	})

	var b strings.Builder
	b.WriteString(" Step impacts:")
	for _, impact := range stepImpacts {
		fmt.Fprintf(&b, "\n• %s %s", impact.Step, formatImpactValue(impact))
	}
	return b.String() + "."
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulingDecisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&decisionsv1alpha1.SchedulingDecision{}).
		Named("schedulingdecision").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Default
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

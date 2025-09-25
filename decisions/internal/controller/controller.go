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

		// Calculate final scores with full pipeline
		finalScores, deletedHosts := r.calculateScores(res.Spec.Input, res.Spec.Pipeline.Outputs)

		// Calculate step-by-step impact for the winner
		stepImpacts := r.calculateStepImpacts(res.Spec.Input, res.Spec.Pipeline.Outputs, finalScores)

		// Find minimal critical path
		criticalSteps, criticalStepCount := r.findCriticalSteps(res.Spec.Input, res.Spec.Pipeline.Outputs, finalScores)

		res.Status.State = v1alpha1.SchedulingDecisionStateResolved
		res.Status.Error = ""

		// Sort finalScores by score (highest to lowest) and generate enhanced description
		orderedScores, description := r.generateOrderedScoresAndDescription(finalScores, res.Spec.Input, criticalSteps, criticalStepCount, len(res.Spec.Pipeline.Outputs), stepImpacts)

		res.Status.FinalScores = orderedScores
		res.Status.DeletedHosts = deletedHosts
		res.Status.Description = description
	}

	if err := r.Status().Update(ctx, &res); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil // No need to requeue.
}

// calculateScores processes pipeline outputs and returns final scores and deleted hosts
func (r *SchedulingDecisionReconciler) calculateScores(input map[string]float64, outputs []v1alpha1.SchedulingDecisionPipelineOutputSpec) (map[string]float64, map[string][]string) {
	finalScores := make(map[string]float64)
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
	baselineWinner := ""
	maxScore := float64(-999999)
	for host, score := range baselineFinalScores {
		if score > maxScore {
			maxScore = score
			baselineWinner = host
		}
	}

	if baselineWinner == "" {
		return []string{}, 0
	}

	criticalSteps := make([]string, 0)

	// Try removing each step one by one
	for i, stepToRemove := range outputs {
		// Create pipeline without this step
		reducedOutputs := make([]v1alpha1.SchedulingDecisionPipelineOutputSpec, 0, len(outputs)-1)
		for j, output := range outputs {
			if j != i {
				reducedOutputs = append(reducedOutputs, output)
			}
		}

		// Calculate scores without this step
		reducedFinalScores, _ := r.calculateScores(input, reducedOutputs)

		// Find winner without this step
		reducedWinner := ""
		reducedMaxScore := float64(-999999)
		for host, score := range reducedFinalScores {
			if score > reducedMaxScore {
				reducedMaxScore = score
				reducedWinner = host
			}
		}

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
	finalWinner := ""
	maxScore := float64(-999999)
	for host, score := range finalScores {
		if score > maxScore {
			maxScore = score
			finalWinner = host
		}
	}

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

	// Sort input scores to determine input-based ranking
	var sortedInputHosts []hostScore
	for host, score := range inputScores {
		sortedInputHosts = append(sortedInputHosts, hostScore{host: host, score: score})
	}
	sort.Slice(sortedInputHosts, func(i, j int) bool {
		return sortedInputHosts[i].score > sortedInputHosts[j].score
	})

	// Find positions and generate comparison
	finalWinner := sortedHosts[0].host
	inputWinner := sortedInputHosts[0].host
	finalWinnerInputScore := inputScores[finalWinner]

	// Find final winner's position in input ranking
	finalWinnerInputPosition := -1
	for i, hs := range sortedInputHosts {
		if hs.host == finalWinner {
			finalWinnerInputPosition = i + 1 // 1-based position
			break
		}
	}

	// Generate main description
	var description string
	if len(sortedHosts) == 1 {
		description = fmt.Sprintf("Selected: %s (score: %.2f), certainty: perfect, %d hosts evaluated.",
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

		description = fmt.Sprintf("Selected: %s (score: %.2f), certainty: %s (gap: %.2f), %d hosts evaluated.",
			sortedHosts[0].host, sortedHosts[0].score, certainty, gap, totalInputHosts)
	}

	// Add input vs. final comparison
	var comparison string
	if inputWinner == finalWinner {
		// Input choice confirmed
		comparison = fmt.Sprintf(" Input choice confirmed: %s (%.2f→%.2f, remained #1).",
			finalWinner, finalWinnerInputScore, sortedHosts[0].score)
	} else {
		// Input winner different from final winner
		inputWinnerScore := sortedInputHosts[0].score

		// Check if input winner was filtered out
		_, inputWinnerSurvived := finalScores[inputWinner]
		if !inputWinnerSurvived {
			comparison = fmt.Sprintf(" Input favored %s (score: %.2f, now filtered), final winner was #%d in input (%.2f→%.2f).",
				inputWinner, inputWinnerScore, finalWinnerInputPosition, finalWinnerInputScore, sortedHosts[0].score)
		} else {
			// Find input winner's position in final ranking
			inputWinnerFinalPosition := -1
			for i, hs := range sortedHosts {
				if hs.host == inputWinner {
					inputWinnerFinalPosition = i + 1 // 1-based position
					break
				}
			}
			comparison = fmt.Sprintf(" Input favored %s (score: %.2f, now #%d with %.2f), final winner was #%d in input (%.2f→%.2f).",
				inputWinner, inputWinnerScore, inputWinnerFinalPosition, finalScores[inputWinner],
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
				// Join critical steps with commas
				stepList := ""
				for i, step := range criticalSteps {
					if i == len(criticalSteps)-1 {
						stepList += step
					} else if i == len(criticalSteps)-2 {
						stepList += step + " and "
					} else {
						stepList += step + ", "
					}
				}
				criticalPath = fmt.Sprintf(" Decision driven by %d/%d pipeline steps: %s.", criticalStepCount, totalSteps, stepList)
			}
		}
	}

	description += comparison + criticalPath + stepImpactInfo
	return orderedScores, description
}

// formatStepImpactsMultiLine formats step impacts in a simple delta-ordered format
// without confusing terminology, ordered by absolute impact magnitude
func (r *SchedulingDecisionReconciler) formatStepImpactsMultiLine(stepImpacts []StepImpact) string {
	if len(stepImpacts) == 0 {
		return ""
	}

	// Create a copy of impacts for sorting
	sortedImpacts := make([]StepImpact, len(stepImpacts))
	copy(sortedImpacts, stepImpacts)

	// Sort by absolute delta impact (highest first), with promotions taking priority for ties
	sort.Slice(sortedImpacts, func(i, j int) bool {
		absI := sortedImpacts[i].ScoreDelta
		if absI < 0 {
			absI = -absI
		}
		absJ := sortedImpacts[j].ScoreDelta
		if absJ < 0 {
			absJ = -absJ
		}

		// First priority: higher absolute delta
		if absI != absJ {
			return absI > absJ
		}

		// Tie-breaking: promotions come first
		if sortedImpacts[i].PromotedToFirst != sortedImpacts[j].PromotedToFirst {
			return sortedImpacts[i].PromotedToFirst
		}

		// Final tie-breaking: maintain original pipeline order (use step name for consistency)
		return sortedImpacts[i].Step < sortedImpacts[j].Step
	})

	var lines []string

	for _, impact := range sortedImpacts {
		var stepDesc string

		if impact.PromotedToFirst {
			// Step promoted winner to first place
			if impact.ScoreDelta != 0 {
				stepDesc = fmt.Sprintf("%s %+.2f→#1", impact.Step, impact.ScoreDelta)
			} else {
				// Zero delta but promoted (must have removed competitors)
				stepDesc = fmt.Sprintf("%s +0.00→#1", impact.Step)
			}
		} else if impact.ScoreDelta != 0 {
			// Step changed winner's score but didn't promote to #1
			stepDesc = fmt.Sprintf("%s %+.2f", impact.Step, impact.ScoreDelta)
		} else if impact.CompetitorsRemoved > 0 {
			// Step removed competitors but didn't change winner's score or promote
			stepDesc = fmt.Sprintf("%s +0.00 (removed %d)", impact.Step, impact.CompetitorsRemoved)
		} else {
			// Step had no measurable impact
			stepDesc = fmt.Sprintf("%s +0.00", impact.Step)
		}

		lines = append(lines, fmt.Sprintf("• %s", stepDesc))
	}

	if len(lines) == 0 {
		return ""
	}

	// Join with newlines and add initial label
	return fmt.Sprintf(" Step impacts:\n%s", joinLines(lines))
}

// joinStepList joins step descriptions with appropriate separators
func joinStepList(steps []string) string {
	if len(steps) == 0 {
		return ""
	}
	if len(steps) == 1 {
		return steps[0]
	}
	if len(steps) == 2 {
		return steps[0] + ", " + steps[1]
	}

	result := ""
	for i, step := range steps {
		if i < len(steps)-1 {
			result += step + ", "
		} else {
			result += step
		}
	}
	return result
}

// joinLines joins multiple lines with newlines and proper indentation
func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i < len(lines)-1 {
			result += line + "\n"
		} else {
			result += line
		}
	}
	return result + "."
}

// joinImpacts joins step impact descriptions with appropriate separators (kept for compatibility)
func joinImpacts(impacts []string) string {
	if len(impacts) == 0 {
		return ""
	}
	if len(impacts) == 1 {
		return impacts[0]
	}
	if len(impacts) == 2 {
		return impacts[0] + ", " + impacts[1]
	}

	result := ""
	for i, impact := range impacts {
		if i == len(impacts)-1 {
			result += impact
		} else {
			result += impact + ", "
		}
	}
	return result
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

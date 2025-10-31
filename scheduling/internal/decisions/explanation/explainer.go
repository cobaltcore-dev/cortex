// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"fmt"
	"sort"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The explainer gets a scheduling decision and produces a human-readable
// explanation of why the decision was made the way it was.
type Explainer struct {
	// The kubernetes client to use for fetching related data.
	client.Client
}

// Explain the given decision and return a human-readable explanation.
func (e *Explainer) Explain(ctx context.Context, decision *v1alpha1.Decision) (string, error) {
	// Build context part
	contextPart := e.buildContext(decision)

	// Build history comparison if there's history
	historyComparison, err := e.buildHistoryComparison(ctx, decision)
	if err != nil {
		return "", err
	}

	// Build winner analysis part
	winnerAnalysis := e.buildWinnerAnalysis(decision)

	// Build input comparison part
	inputComparison := e.buildInputComparison(decision)

	// Build critical steps analysis part
	criticalSteps := e.buildCriticalStepsAnalysis(decision)

	// Build deleted hosts analysis part
	deletedHostsAnalysis := e.buildDeletedHostsAnalysis(decision)

	// Combine parts
	var parts []string
	parts = append(parts, contextPart)

	if historyComparison != "" {
		parts = append(parts, historyComparison)
	}

	if winnerAnalysis != "" {
		parts = append(parts, winnerAnalysis)
	}

	if inputComparison != "" {
		parts = append(parts, inputComparison)
	}

	if criticalSteps != "" {
		parts = append(parts, criticalSteps)
	}

	if deletedHostsAnalysis != "" {
		parts = append(parts, deletedHostsAnalysis)
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += " " + parts[i]
	}

	return result, nil
}

// buildContext creates the contextual part of the explanation.
func (e *Explainer) buildContext(decision *v1alpha1.Decision) string {
	resourceType := e.getResourceType(decision.Spec.Type)

	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		return fmt.Sprintf("Initial placement of the %s.", resourceType)
	}

	precedence := len(*history) + 1
	if decision.Status.Precedence != nil {
		precedence = *decision.Status.Precedence + 1
	}

	return fmt.Sprintf("Decision #%d for this %s.", precedence, resourceType)
}

// buildHistoryComparison creates the history comparison part of the explanation.
func (e *Explainer) buildHistoryComparison(ctx context.Context, decision *v1alpha1.Decision) (string, error) {
	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		return "", nil
	}

	// Get the last decision
	lastDecisionRef := (*history)[len(*history)-1]
	lastDecision := &v1alpha1.Decision{}
	if err := e.Get(ctx, client.ObjectKey{
		Namespace: lastDecisionRef.Namespace,
		Name:      lastDecisionRef.Name,
	}, lastDecision); err != nil {
		return "", err
	}

	lastTarget := "(n/a)"
	if lastDecision.Status.Result != nil && lastDecision.Status.Result.TargetHost != nil {
		lastTarget = *lastDecision.Status.Result.TargetHost
	}

	newTarget := "(n/a)"
	if decision.Status.Result != nil && decision.Status.Result.TargetHost != nil {
		newTarget = *decision.Status.Result.TargetHost
	}

	return fmt.Sprintf("Previous target host was '%s', now it's '%s'.", lastTarget, newTarget), nil
}

// getResourceType returns a human-readable resource type.
func (e *Explainer) getResourceType(decisionType v1alpha1.DecisionType) string {
	switch decisionType {
	case v1alpha1.DecisionTypeNovaServer:
		return "nova server"
	case v1alpha1.DecisionTypeManilaShare:
		return "manila share"
	case v1alpha1.DecisionTypeCinderVolume:
		return "cinder volume"
	case v1alpha1.DecisionTypeIroncoreMachine:
		return "ironcore machine"
	default:
		return "resource"
	}
}

// buildWinnerAnalysis creates the winner analysis part of the explanation.
func (e *Explainer) buildWinnerAnalysis(decision *v1alpha1.Decision) string {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil {
		return ""
	}

	targetHost := *result.TargetHost

	// Get target host score
	targetScore := 0.0
	if result.AggregatedOutWeights != nil {
		if score, exists := result.AggregatedOutWeights[targetHost]; exists {
			targetScore = score
		}
	}

	// Count hosts evaluated
	hostsEvaluated := len(result.OrderedHosts)
	if hostsEvaluated == 0 && result.AggregatedOutWeights != nil {
		hostsEvaluated = len(result.AggregatedOutWeights)
	}

	// Calculate score gap to second place
	gap := e.calculateScoreGap(result.AggregatedOutWeights)

	if gap == 0.0 {
		// Only one host or perfect score
		return fmt.Sprintf("Selected: %s (score: %.2f), %d hosts evaluated.",
			targetHost, targetScore, hostsEvaluated)
	}

	return fmt.Sprintf("Selected: %s (score: %.2f), gap to 2nd: %.2f, %d hosts evaluated.",
		targetHost, targetScore, gap, hostsEvaluated)
}

// calculateScoreGap calculates the gap between first and second place.
func (e *Explainer) calculateScoreGap(weights map[string]float64) float64 {
	if weights == nil || len(weights) < 2 {
		return 0.0
	}

	// Sort scores descending
	scores := make([]float64, 0, len(weights))
	for _, score := range weights {
		scores = append(scores, score)
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i] > scores[j]
	})

	return scores[0] - scores[1]
}

// buildInputComparison creates the input vs output comparison part of the explanation.
func (e *Explainer) buildInputComparison(decision *v1alpha1.Decision) string {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil {
		return ""
	}

	targetHost := *result.TargetHost

	// Get input weights (prefer normalized, fall back to raw)
	var inputWeights map[string]float64
	if result.NormalizedInWeights != nil && len(result.NormalizedInWeights) > 0 {
		inputWeights = result.NormalizedInWeights
	} else if result.RawInWeights != nil && len(result.RawInWeights) > 0 {
		inputWeights = result.RawInWeights
	} else {
		return ""
	}

	// Find input winner
	inputWinner := ""
	inputWinnerScore := -999999.0
	for host, score := range inputWeights {
		if score > inputWinnerScore {
			inputWinnerScore = score
			inputWinner = host
		}
	}

	if inputWinner == "" {
		return ""
	}

	// Get target host's input and final scores
	targetInputScore := 0.0
	if score, exists := inputWeights[targetHost]; exists {
		targetInputScore = score
	}

	targetFinalScore := 0.0
	if result.AggregatedOutWeights != nil {
		if score, exists := result.AggregatedOutWeights[targetHost]; exists {
			targetFinalScore = score
		}
	}

	if inputWinner == targetHost {
		return fmt.Sprintf("Input choice confirmed: %s (%.2f→%.2f).",
			targetHost, targetInputScore, targetFinalScore)
	}

	// Input winner different from final winner
	return fmt.Sprintf("Input favored %s (%.2f), final winner: %s (%.2f→%.2f).",
		inputWinner, inputWinnerScore, targetHost, targetInputScore, targetFinalScore)
}

// buildCriticalStepsAnalysis creates the critical steps analysis part of the explanation.
func (e *Explainer) buildCriticalStepsAnalysis(decision *v1alpha1.Decision) string {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil || result.StepResults == nil || len(result.StepResults) == 0 {
		return ""
	}

	targetHost := *result.TargetHost
	criticalSteps := e.findCriticalSteps(decision, targetHost)

	if len(criticalSteps) == 0 {
		totalSteps := len(result.StepResults)
		return fmt.Sprintf("Decision driven by input only (all %d steps are non-critical).", totalSteps)
	}

	totalSteps := len(result.StepResults)
	if len(criticalSteps) == totalSteps {
		return fmt.Sprintf("Decision requires all %d pipeline steps.", totalSteps)
	}

	if len(criticalSteps) == 1 {
		return fmt.Sprintf("Decision driven by 1/%d pipeline step: %s.", totalSteps, criticalSteps[0])
	}

	// Multiple critical steps
	var stepList string
	if len(criticalSteps) == 2 {
		stepList = criticalSteps[0] + " and " + criticalSteps[1]
	} else {
		// For 3+ steps: "step1, step2, and step3"
		lastStep := criticalSteps[len(criticalSteps)-1]
		otherSteps := criticalSteps[:len(criticalSteps)-1]
		stepList = ""
		for i, step := range otherSteps {
			if i > 0 {
				stepList += ", "
			}
			stepList += step
		}
		stepList += " and " + lastStep
	}

	return fmt.Sprintf("Decision driven by %d/%d pipeline steps: %s.", len(criticalSteps), totalSteps, stepList)
}

// buildDeletedHostsAnalysis creates the deleted hosts analysis part of the explanation.
func (e *Explainer) buildDeletedHostsAnalysis(decision *v1alpha1.Decision) string {
	result := decision.Status.Result
	if result == nil || result.StepResults == nil || len(result.StepResults) == 0 {
		return ""
	}

	// Get input weights (prefer normalized, fall back to raw)
	var inputWeights map[string]float64
	if result.NormalizedInWeights != nil && len(result.NormalizedInWeights) > 0 {
		inputWeights = result.NormalizedInWeights
	} else if result.RawInWeights != nil && len(result.RawInWeights) > 0 {
		inputWeights = result.RawInWeights
	} else {
		return ""
	}

	// Calculate scores and get deleted hosts information
	scoreResult := e.calculateScoresFromSteps(inputWeights, result.StepResults)

	if len(scoreResult.DeletedHosts) == 0 {
		return ""
	}

	// Check if input winner was deleted
	inputWinner := ""
	inputWinnerScore := -999999.0
	for host, score := range inputWeights {
		if score > inputWinnerScore {
			inputWinnerScore = score
			inputWinner = host
		}
	}

	// Build deleted hosts summary
	totalDeleted := len(scoreResult.DeletedHosts)
	if totalDeleted == 1 {
		// Single host deleted
		for hostName, steps := range scoreResult.DeletedHosts {
			if len(steps) == 1 {
				if hostName == inputWinner {
					return fmt.Sprintf("Input winner %s was filtered by %s.", hostName, steps[0])
				}
				return fmt.Sprintf("Host %s was filtered by %s.", hostName, steps[0])
			} else {
				// Multiple steps deleted the same host (shouldn't happen in normal flow)
				stepList := e.formatStepList(steps)
				if hostName == inputWinner {
					return fmt.Sprintf("Input winner %s was filtered by %s.", hostName, stepList)
				}
				return fmt.Sprintf("Host %s was filtered by %s.", hostName, stepList)
			}
		}
	} else {
		// Multiple hosts deleted
		inputWinnerDeleted := false
		if _, exists := scoreResult.DeletedHosts[inputWinner]; exists {
			inputWinnerDeleted = true
		}

		if inputWinnerDeleted {
			return fmt.Sprintf("%d hosts filtered (including input winner %s).", totalDeleted, inputWinner)
		}
		return fmt.Sprintf("%d hosts filtered.", totalDeleted)
	}

	return ""
}

// formatStepList formats a list of step names with proper grammar.
func (e *Explainer) formatStepList(steps []string) string {
	if len(steps) == 0 {
		return ""
	}
	if len(steps) == 1 {
		return steps[0]
	}
	if len(steps) == 2 {
		return steps[0] + " and " + steps[1]
	}

	// For 3+ steps: "step1, step2, and step3"
	lastStep := steps[len(steps)-1]
	otherSteps := steps[:len(steps)-1]
	result := ""
	for i, step := range otherSteps {
		if i > 0 {
			result += ", "
		}
		result += step
	}
	result += " and " + lastStep
	return result
}

// findWinner returns the host with the highest score and the score value.
func (e *Explainer) findWinner(scores map[string]float64) (string, float64) {
	if len(scores) == 0 {
		return "", -999999.0
	}

	winner := ""
	maxScore := -999999.0
	for host, score := range scores {
		if score > maxScore {
			maxScore = score
			winner = host
		}
	}
	return winner, maxScore
}

// ScoreCalculationResult holds both final scores and deleted host tracking information.
type ScoreCalculationResult struct {
	FinalScores  map[string]float64
	DeletedHosts map[string][]string // host -> list of steps that deleted it
}

// calculateScoresFromSteps processes step results sequentially to compute final scores and track deleted hosts.
func (e *Explainer) calculateScoresFromSteps(inputWeights map[string]float64, stepResults []v1alpha1.StepResult) ScoreCalculationResult {
	if len(inputWeights) == 0 {
		return ScoreCalculationResult{
			FinalScores:  map[string]float64{},
			DeletedHosts: map[string][]string{},
		}
	}

	// Start with input values as initial scores
	currentScores := make(map[string]float64)
	for hostName, inputValue := range inputWeights {
		currentScores[hostName] = inputValue
	}

	deletedHosts := make(map[string][]string)

	// Process each step sequentially
	for _, stepResult := range stepResults {
		// Check which hosts will be deleted in this step
		for hostName := range currentScores {
			if _, exists := stepResult.Activations[hostName]; !exists {
				// Host not in this step's activations - will be deleted
				deletedHosts[hostName] = append(deletedHosts[hostName], stepResult.StepRef.Name)
			}
		}

		// Apply activations and remove hosts not in this step
		newScores := make(map[string]float64)
		for hostName, score := range currentScores {
			if activation, exists := stepResult.Activations[hostName]; exists {
				// Add activation to current score
				newScores[hostName] = score + activation
			}
			// Hosts not in activations are removed (don't copy to newScores)
		}
		currentScores = newScores
	}

	return ScoreCalculationResult{
		FinalScores:  currentScores,
		DeletedHosts: deletedHosts,
	}
}

// calculateScoresWithoutStep processes step results while skipping one specific step.
func (e *Explainer) calculateScoresWithoutStep(inputWeights map[string]float64, stepResults []v1alpha1.StepResult, skipIndex int) ScoreCalculationResult {
	if len(inputWeights) == 0 || skipIndex < 0 || skipIndex >= len(stepResults) {
		return e.calculateScoresFromSteps(inputWeights, stepResults)
	}

	// Create reduced step results without the skipped step
	reducedSteps := make([]v1alpha1.StepResult, 0, len(stepResults)-1)
	reducedSteps = append(reducedSteps, stepResults[:skipIndex]...)
	reducedSteps = append(reducedSteps, stepResults[skipIndex+1:]...)

	return e.calculateScoresFromSteps(inputWeights, reducedSteps)
}

// findCriticalSteps determines which steps change the winning host using backward elimination.
func (e *Explainer) findCriticalSteps(decision *v1alpha1.Decision, targetHost string) []string {
	result := decision.Status.Result
	if result == nil || result.StepResults == nil || len(result.StepResults) == 0 {
		return []string{}
	}

	// Get input weights (prefer normalized, fall back to raw)
	var inputWeights map[string]float64
	if result.NormalizedInWeights != nil && len(result.NormalizedInWeights) > 0 {
		inputWeights = result.NormalizedInWeights
	} else if result.RawInWeights != nil && len(result.RawInWeights) > 0 {
		inputWeights = result.RawInWeights
	} else {
		return []string{}
	}

	// Calculate baseline scores with all steps
	baselineResult := e.calculateScoresFromSteps(inputWeights, result.StepResults)
	baselineWinner, _ := e.findWinner(baselineResult.FinalScores)

	if baselineWinner == "" {
		return []string{}
	}

	criticalSteps := make([]string, 0)

	// Try removing each step one by one
	for i, stepResult := range result.StepResults {
		// Calculate scores without this step
		reducedResult := e.calculateScoresWithoutStep(inputWeights, result.StepResults, i)

		// Find winner without this step
		reducedWinner, _ := e.findWinner(reducedResult.FinalScores)

		// If removing this step changes the winner, it's critical
		if reducedWinner != baselineWinner {
			criticalSteps = append(criticalSteps, stepResult.StepRef.Name)
		}
	}

	return criticalSteps
}

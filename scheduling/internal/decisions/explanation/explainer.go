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

// findCriticalSteps identifies which steps had significant impact on the target host.
// Uses a simplified heuristic based on activation magnitudes rather than complex elimination testing.
func (e *Explainer) findCriticalSteps(decision *v1alpha1.Decision, targetHost string) []string {
	result := decision.Status.Result
	if result == nil || result.StepResults == nil {
		return []string{}
	}

	// Collect step activations for the target host
	type stepActivation struct {
		stepName   string
		activation float64
	}

	var activations []stepActivation
	for _, stepResult := range result.StepResults {
		if activation, exists := stepResult.Activations[targetHost]; exists {
			activations = append(activations, stepActivation{
				stepName:   stepResult.StepRef.Name,
				activation: activation,
			})
		}
	}

	if len(activations) == 0 {
		return []string{}
	}

	// Sort by absolute activation value (highest impact first)
	sort.Slice(activations, func(i, j int) bool {
		absI := activations[i].activation
		if absI < 0 {
			absI = -absI
		}
		absJ := activations[j].activation
		if absJ < 0 {
			absJ = -absJ
		}
		return absI > absJ
	})

	// Consider steps with significant activation as critical
	// Use a simple threshold: steps with activation >= 0.1 or top 50% of steps
	var criticalSteps []string
	threshold := 0.1
	maxSteps := (len(activations) + 1) / 2 // At least half the steps

	for i, activation := range activations {
		absActivation := activation.activation
		if absActivation < 0 {
			absActivation = -absActivation
		}

		if absActivation >= threshold || i < maxSteps {
			criticalSteps = append(criticalSteps, activation.stepName)
		}
	}

	return criticalSteps
}

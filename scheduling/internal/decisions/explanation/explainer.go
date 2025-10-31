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

	// Combine parts
	var parts []string
	parts = append(parts, contextPart)

	if historyComparison != "" {
		parts = append(parts, historyComparison)
	}

	if winnerAnalysis != "" {
		parts = append(parts, winnerAnalysis)
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

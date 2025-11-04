// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Explainer struct {
	client.Client
}

func (e *Explainer) Explain(ctx context.Context, decision *v1alpha1.Decision) (string, error) {
	contextPart := e.buildContext(decision)

	historyComparison, err := e.buildHistoryComparison(ctx, decision)
	if err != nil {
		return "", err
	}

	winnerAnalysis := e.buildWinnerAnalysis(decision)
	inputComparison := e.buildInputComparison(decision)
	criticalSteps := e.buildCriticalStepsAnalysis(decision)
	deletedHostsAnalysis := e.buildDeletedHostsAnalysis(decision)
	stepImpactAnalysis := e.buildStepImpactAnalysis(decision)

	globalChainAnalysis, err := e.buildGlobalChainAnalysis(ctx, decision)
	if err != nil {
		return "", err
	}

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
	if stepImpactAnalysis != "" {
		parts = append(parts, stepImpactAnalysis)
	}
	if globalChainAnalysis != "" {
		parts = append(parts, globalChainAnalysis)
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += " " + parts[i]
	}

	return result, nil
}

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
		logger := log.FromContext(ctx)
		if errors.IsNotFound(err) {
			logger.Info("History decision not found, skipping history comparison",
				"decision", lastDecisionRef.Name,
				"namespace", lastDecisionRef.Namespace,
				"uid", lastDecisionRef.UID)
			return "", nil // Skip history comparison instead of failing
		}
		// For other errors, still fail
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
	if len(weights) < 2 {
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

	// Get input weights (prefer raw, fall back to normalized)
	var inputWeights map[string]float64
	switch {
	case len(result.RawInWeights) > 0:
		inputWeights = result.RawInWeights
	case len(result.NormalizedInWeights) > 0:
		inputWeights = result.NormalizedInWeights
	default:
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
	if result == nil || result.TargetHost == nil || len(result.StepResults) == 0 {
		return ""
	}

	criticalSteps := e.findCriticalSteps(decision)

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

	// Get input weights (prefer raw, fall back to normalized)
	var inputWeights map[string]float64
	switch {
	case len(result.RawInWeights) > 0:
		inputWeights = result.RawInWeights
	case len(result.NormalizedInWeights) > 0:
		inputWeights = result.NormalizedInWeights
	default:
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

// buildGlobalChainAnalysis creates the global chain analysis part of the explanation.
func (e *Explainer) buildGlobalChainAnalysis(ctx context.Context, decision *v1alpha1.Decision) (string, error) {
	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		return "", nil // No chain for initial decisions
	}

	// Fetch all decisions in the chain
	chainDecisions, err := e.fetchDecisionChain(ctx, decision)
	if err != nil {
		return "", err
	}

	if len(chainDecisions) < 2 {
		return "", nil // Need at least 2 decisions for a chain
	}

	// Generate chain description
	return e.generateChainDescription(chainDecisions), nil
}

// fetchDecisionChain retrieves all decisions in the history chain.
func (e *Explainer) fetchDecisionChain(ctx context.Context, decision *v1alpha1.Decision) ([]*v1alpha1.Decision, error) {
	var chainDecisions []*v1alpha1.Decision
	logger := log.FromContext(ctx)

	// Add all historical decisions
	if decision.Status.History != nil {
		for _, ref := range *decision.Status.History {
			histDecision := &v1alpha1.Decision{}
			if err := e.Get(ctx, client.ObjectKey{
				Namespace: ref.Namespace,
				Name:      ref.Name,
			}, histDecision); err != nil {
				if errors.IsNotFound(err) {
					logger.Info("History decision not found, skipping from chain analysis",
						"decision", ref.Name,
						"namespace", ref.Namespace,
						"uid", ref.UID)
					continue // Skip missing decisions instead of failing
				}
				// For other errors, still fail
				return nil, err
			}
			chainDecisions = append(chainDecisions, histDecision)
		}
	}

	// Add current decision
	chainDecisions = append(chainDecisions, decision)

	return chainDecisions, nil
}

// generateChainDescription creates a chain description from a sequence of decisions.
func (e *Explainer) generateChainDescription(decisions []*v1alpha1.Decision) string {
	if len(decisions) < 2 {
		return ""
	}

	// Extract host chain and build segments
	segments := e.buildHostSegments(decisions)
	if len(segments) == 0 {
		return ""
	}

	// Build chain string with durations
	chainParts := make([]string, 0, len(segments))
	for _, segment := range segments {
		part := segment.host + " (" + (time.Duration(int(segment.duration.Seconds())) * time.Second).String()
		if segment.decisions > 1 {
			part += fmt.Sprintf("; %d decisions", segment.decisions)
		}
		part += ")"
		chainParts = append(chainParts, part)
	}

	// Check for loops
	hasLoop := e.detectLoop(segments)
	chainStr := e.joinChainParts(chainParts)

	if hasLoop {
		return fmt.Sprintf("Chain (loop detected): %s.", chainStr)
	}
	return fmt.Sprintf("Chain: %s.", chainStr)
}

// HostSegment represents a segment in the host chain with duration and decision count.
type HostSegment struct {
	host      string
	duration  time.Duration // Full precision duration
	decisions int
}

// buildHostSegments creates host segments from decisions with durations.
func (e *Explainer) buildHostSegments(decisions []*v1alpha1.Decision) []HostSegment {
	if len(decisions) < 2 {
		return []HostSegment{}
	}

	// Extract host chain
	hostChain := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		host := "(n/a)"
		if decision.Status.Result != nil && decision.Status.Result.TargetHost != nil {
			host = *decision.Status.Result.TargetHost
		}
		hostChain = append(hostChain, host)
	}

	// Build segments with durations
	segments := make([]HostSegment, 0)
	if len(hostChain) > 0 {
		currentHost := hostChain[0]
		segmentStart := 0

		for i := 1; i <= len(hostChain); i++ {
			// Check if we've reached the end or found a different host
			if i == len(hostChain) || hostChain[i] != currentHost {
				// Calculate duration for this segment
				startTime := decisions[segmentStart].CreationTimestamp.Time
				var endTime = startTime // Default to 0 duration for last segment
				if i < len(hostChain) {
					endTime = decisions[i].CreationTimestamp.Time
				}

				duration := endTime.Sub(startTime)

				segments = append(segments, HostSegment{
					host:      currentHost,
					duration:  duration,
					decisions: i - segmentStart,
				})

				if i < len(hostChain) {
					currentHost = hostChain[i]
					segmentStart = i
				}
			}
		}
	}

	return segments
}

// detectLoop checks if there are repeated hosts in the segments.
func (e *Explainer) detectLoop(segments []HostSegment) bool {
	seenHosts := make(map[string]bool)
	for _, segment := range segments {
		if seenHosts[segment.host] {
			return true
		}
		seenHosts[segment.host] = true
	}
	return false
}

// joinChainParts joins chain parts with arrows.
func (e *Explainer) joinChainParts(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += " -> "
		}
		result += part
	}
	return result
}

// findWinner returns the host with the highest score.
func (e *Explainer) findWinner(scores map[string]float64) string {
	winner := ""
	maxScore := -999999.0
	for host, score := range scores {
		if score > maxScore {
			maxScore = score
			winner = host
		}
	}
	return winner
}

// ScoreCalculationResult holds both final scores and deleted host tracking information.
type ScoreCalculationResult struct {
	FinalScores  map[string]float64
	DeletedHosts map[string][]string // host -> list of steps that deleted it
}

// StepImpact represents the impact of a single pipeline step on the winning host.
type StepImpact struct {
	Step               string
	ScoreBefore        float64
	ScoreAfter         float64
	ScoreDelta         float64
	CompetitorsRemoved int
	PromotedToFirst    bool
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
func (e *Explainer) findCriticalSteps(decision *v1alpha1.Decision) []string {
	result := decision.Status.Result
	if result == nil || len(result.StepResults) == 0 {
		return []string{}
	}

	// Get input weights (prefer raw, fall back to normalized)
	var inputWeights map[string]float64
	switch {
	case len(result.RawInWeights) > 0:
		inputWeights = result.RawInWeights
	case len(result.NormalizedInWeights) > 0:
		inputWeights = result.NormalizedInWeights
	default:
		return []string{}
	}

	// Calculate baseline scores with all steps
	baselineResult := e.calculateScoresFromSteps(inputWeights, result.StepResults)
	baselineWinner := e.findWinner(baselineResult.FinalScores)

	if baselineWinner == "" {
		return []string{}
	}

	criticalSteps := make([]string, 0)

	// Try removing each step one by one
	for i, stepResult := range result.StepResults {
		// Calculate scores without this step
		reducedResult := e.calculateScoresWithoutStep(inputWeights, result.StepResults, i)

		// Find winner without this step
		reducedWinner := e.findWinner(reducedResult.FinalScores)

		// If removing this step changes the winner, it's critical
		if reducedWinner != baselineWinner {
			criticalSteps = append(criticalSteps, stepResult.StepRef.Name)
		}
	}

	return criticalSteps
}

// buildStepImpactAnalysis creates the step impact analysis part of the explanation.
func (e *Explainer) buildStepImpactAnalysis(decision *v1alpha1.Decision) string {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil || len(result.StepResults) == 0 {
		return ""
	}

	targetHost := *result.TargetHost

	// Get input weights (prefer raw, fall back to normalized)
	var inputWeights map[string]float64
	switch {
	case len(result.RawInWeights) > 0:
		inputWeights = result.RawInWeights
	case len(result.NormalizedInWeights) > 0:
		inputWeights = result.NormalizedInWeights
	default:
		return ""
	}

	// Calculate step impacts for the winning host
	impacts := e.calculateStepImpacts(inputWeights, result.StepResults, targetHost)
	if len(impacts) == 0 {
		return ""
	}

	// Sort impacts by absolute delta (highest first), with promotions taking priority
	sort.Slice(impacts, func(i, j int) bool {
		absI := impacts[i].ScoreDelta
		if absI < 0 {
			absI = -absI
		}
		absJ := impacts[j].ScoreDelta
		if absJ < 0 {
			absJ = -absJ
		}

		if absI != absJ {
			return absI > absJ
		}
		if impacts[i].PromotedToFirst != impacts[j].PromotedToFirst {
			return impacts[i].PromotedToFirst
		}
		return impacts[i].Step < impacts[j].Step
	})

	// Format output
	var parts []string
	for _, impact := range impacts {
		parts = append(parts, e.formatStepImpact(impact))
	}

	output := " Step impacts:\n• " + parts[0]
	for i := 1; i < len(parts); i++ {
		output += "\n• " + parts[i]
	}
	return output + "."
}

// calculateStepImpacts tracks how each pipeline step affects the target host.
func (e *Explainer) calculateStepImpacts(inputWeights map[string]float64, stepResults []v1alpha1.StepResult, targetHost string) []StepImpact {
	if len(inputWeights) == 0 || len(stepResults) == 0 {
		return []StepImpact{}
	}

	impacts := make([]StepImpact, 0, len(stepResults))
	currentScores := make(map[string]float64)

	// Start with input values as initial scores
	for hostName, inputValue := range inputWeights {
		currentScores[hostName] = inputValue
	}

	// Track target host's score before first step
	scoreBefore := currentScores[targetHost]

	// Process each pipeline step and track the target host's evolution
	for _, stepResult := range stepResults {
		// Count how many competitors will be removed in this step
		competitorsRemoved := 0
		for hostName := range currentScores {
			if hostName != targetHost {
				if _, exists := stepResult.Activations[hostName]; !exists {
					competitorsRemoved++
				}
			}
		}

		// Check if target host was #1 before this step
		wasFirst := true
		targetScoreBefore := currentScores[targetHost]
		for host, score := range currentScores {
			if host != targetHost && score > targetScoreBefore {
				wasFirst = false
				break
			}
		}

		// Apply activations and remove hosts not in this step
		newScores := make(map[string]float64)
		for hostName, score := range currentScores {
			if activation, exists := stepResult.Activations[hostName]; exists {
				newScores[hostName] = score + activation
			}
			// Hosts not in activations are removed (don't copy to newScores)
		}

		// Get target host's score after this step
		scoreAfter := newScores[targetHost]

		// Check if target host became #1 after this step
		isFirstAfter := true
		for host, score := range newScores {
			if host != targetHost && score > scoreAfter {
				isFirstAfter = false
				break
			}
		}

		promotedToFirst := !wasFirst && isFirstAfter

		impacts = append(impacts, StepImpact{
			Step:               stepResult.StepRef.Name,
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

	return impacts
}

// formatStepImpact formats a single step impact value.
func (e *Explainer) formatStepImpact(impact StepImpact) string {
	if impact.PromotedToFirst {
		return fmt.Sprintf("%s %+.2f→#1", impact.Step, impact.ScoreDelta)
	}
	if impact.ScoreDelta != 0 {
		return fmt.Sprintf("%s %+.2f", impact.Step, impact.ScoreDelta)
	}
	if impact.CompetitorsRemoved > 0 {
		return fmt.Sprintf("%s +0.00 (removed %d)", impact.Step, impact.CompetitorsRemoved)
	}
	return impact.Step + " +0.00"
}

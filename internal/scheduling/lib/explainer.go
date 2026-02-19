// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// The explainer gets a scheduling decision and produces a human-readable
// explanation of why the decision was made the way it was.
type Explainer struct {
	// The kubernetes client to use for fetching related data.
	client.Client
	// The template manager to use for rendering explanations.
	templateManager *TemplateManager
}

// NewExplainer creates a new explainer with template support.
func NewExplainer(client client.Client) (*Explainer, error) {
	templateManager, err := NewTemplateManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create template manager: %w", err)
	}

	return &Explainer{
		Client:          client,
		templateManager: templateManager,
	}, nil
}

// Explain the given decision and return a human-readable explanation.
func (e *Explainer) Explain(ctx context.Context, decision DecisionUpdate) (string, error) {
	return e.ExplainWithTemplates(ctx, decision)
}

// getResourceType returns a human-readable resource type.
func (e *Explainer) getResourceType(schedulingDomain v1alpha1.SchedulingDomain) string {
	switch schedulingDomain {
	case v1alpha1.SchedulingDomainNova:
		return "nova server"
	case v1alpha1.SchedulingDomainManila:
		return "manila share"
	case v1alpha1.SchedulingDomainCinder:
		return "cinder volume"
	case v1alpha1.SchedulingDomainMachines:
		return "ironcore machine"
	case v1alpha1.SchedulingDomainPods:
		return "pod"
	default:
		return "resource"
	}
}

// calculateScoreGap calculates the gap between first and second place.
func (e *Explainer) calculateScoreGap(weights map[string]float64) float64 {
	if len(weights) < 2 {
		return 0.0
	}

	scores := make([]float64, 0, len(weights))
	for _, score := range weights {
		scores = append(scores, score)
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i] > scores[j]
	})

	return scores[0] - scores[1]
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
				deletedHosts[hostName] = append(deletedHosts[hostName], stepResult.StepName)
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
			criticalSteps = append(criticalSteps, stepResult.StepName)
		}
	}

	return criticalSteps
}

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
			Step:               stepResult.StepName,
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

// Template data building functions - these functions extract and structure
// decision data into formats suitable for template rendering.

// buildContextData creates context data for template rendering.
func (e *Explainer) buildContextData(decision DecisionUpdate) ContextData {
	resourceType := e.getResourceType(decision.Spec.SchedulingDomain)

	history := decision.Status.History
	isInitial := history == nil || len(*history) == 0

	decisionNumber := 1
	if !isInitial {
		decisionNumber = len(*history) + 1
		if decision.Status.Precedence != nil {
			decisionNumber = *decision.Status.Precedence + 1
		}
	}

	return ContextData{
		ResourceType:   resourceType,
		DecisionNumber: decisionNumber,
		IsInitial:      isInitial,
	}
}

// buildHistoryData creates history comparison data for template rendering.
func (e *Explainer) buildHistoryData(ctx context.Context, decision *v1alpha1.Decision) (*HistoryData, error) {
	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		return nil, nil
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
			return nil, nil // Skip history comparison instead of failing
		}
		// For other errors, still fail
		return nil, err
	}

	lastTarget := "(n/a)"
	if lastDecision.Status.Result != nil && lastDecision.Status.Result.TargetHost != nil {
		lastTarget = *lastDecision.Status.Result.TargetHost
	}

	newTarget := "(n/a)"
	if decision.Status.Result != nil && decision.Status.Result.TargetHost != nil {
		newTarget = *decision.Status.Result.TargetHost
	}

	return &HistoryData{
		PreviousTarget: lastTarget,
		CurrentTarget:  newTarget,
	}, nil
}

// buildWinnerData creates winner analysis data for template rendering.
func (e *Explainer) buildWinnerData(decision *v1alpha1.Decision) *WinnerData {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil {
		return nil
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

	return &WinnerData{
		HostName:       targetHost,
		Score:          targetScore,
		Gap:            gap,
		HostsEvaluated: hostsEvaluated,
		HasGap:         gap > 0,
	}
}

// buildInputData creates input comparison data for template rendering.
func (e *Explainer) buildInputData(decision *v1alpha1.Decision) *InputData {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil {
		return nil
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
		return nil
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
		return nil
	}

	// Get target host's final score
	targetFinalScore := 0.0
	if result.AggregatedOutWeights != nil {
		if score, exists := result.AggregatedOutWeights[targetHost]; exists {
			targetFinalScore = score
		}
	}

	return &InputData{
		InputWinner:     inputWinner,
		InputScore:      inputWinnerScore,
		FinalWinner:     targetHost,
		FinalScore:      targetFinalScore,
		FinalInputScore: inputWeights[targetHost],
		InputConfirmed:  inputWinner == targetHost,
	}
}

// buildCriticalStepsData creates critical steps data for template rendering.
func (e *Explainer) buildCriticalStepsData(decision *v1alpha1.Decision) *CriticalStepsData {
	result := decision.Status.Result
	if result == nil || result.TargetHost == nil || len(result.StepResults) == 0 {
		return nil
	}

	criticalSteps := e.findCriticalSteps(decision)
	totalSteps := len(result.StepResults)

	return &CriticalStepsData{
		Steps:       criticalSteps,
		TotalSteps:  totalSteps,
		IsInputOnly: len(criticalSteps) == 0,
		RequiresAll: len(criticalSteps) == totalSteps,
	}
}

// buildDeletedHostsData creates deleted hosts data for template rendering.
func (e *Explainer) buildDeletedHostsData(decision *v1alpha1.Decision) *DeletedHostsData {
	result := decision.Status.Result
	if result == nil || result.StepResults == nil || len(result.StepResults) == 0 {
		return nil
	}

	// Get input weights (prefer raw, fall back to normalized)
	var inputWeights map[string]float64
	switch {
	case len(result.RawInWeights) > 0:
		inputWeights = result.RawInWeights
	case len(result.NormalizedInWeights) > 0:
		inputWeights = result.NormalizedInWeights
	default:
		return nil
	}

	// Calculate scores and get deleted hosts information
	scoreResult := e.calculateScoresFromSteps(inputWeights, result.StepResults)

	if len(scoreResult.DeletedHosts) == 0 {
		return nil
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

	// Build list of deleted hosts
	deletedHosts := make([]DeletedHostInfo, 0, len(scoreResult.DeletedHosts))
	for hostName, steps := range scoreResult.DeletedHosts {
		deletedHosts = append(deletedHosts, DeletedHostInfo{
			Name:          hostName,
			Steps:         steps,
			IsInputWinner: hostName == inputWinner,
		})
	}

	return &DeletedHostsData{
		DeletedHosts: deletedHosts,
	}
}

// buildChainData creates chain analysis data for template rendering.
func (e *Explainer) buildChainData(ctx context.Context, decision *v1alpha1.Decision) (*ChainData, error) {
	history := decision.Status.History
	if history == nil || len(*history) == 0 {
		return nil, nil // No chain for initial decisions
	}

	// Fetch all decisions in the chain
	chainDecisions, err := e.fetchDecisionChain(ctx, decision)
	if err != nil {
		return nil, err
	}

	if len(chainDecisions) < 2 {
		return nil, nil // Need at least 2 decisions for a chain
	}

	// Build segments
	segments := e.buildHostSegments(chainDecisions)
	if len(segments) == 0 {
		return nil, nil
	}

	// Convert to template data format
	chainSegments := make([]ChainSegment, len(segments))
	for i, segment := range segments {
		chainSegments[i] = ChainSegment{
			Host:      segment.host,
			Duration:  segment.duration,
			Decisions: segment.decisions,
		}
	}

	return &ChainData{
		Segments: chainSegments,
		HasLoop:  e.detectLoop(segments),
	}, nil
}

// ExplainWithTemplates renders an explanation using Go templates.
func (e *Explainer) ExplainWithTemplates(ctx context.Context, decision DecisionUpdate) (string, error) {
	// Build explanation context
	explanationCtx := ExplanationContext{
		Context: e.buildContextData(decision),
	}

	// Build each component's data
	if historyData, err := e.buildHistoryData(ctx, decision); err != nil {
		return "", err
	} else if historyData != nil {
		explanationCtx.History = historyData
	}

	if winnerData := e.buildWinnerData(decision); winnerData != nil {
		explanationCtx.Winner = winnerData
	}

	if inputData := e.buildInputData(decision); inputData != nil {
		explanationCtx.Input = inputData
	}

	if criticalStepsData := e.buildCriticalStepsData(decision); criticalStepsData != nil {
		explanationCtx.CriticalSteps = criticalStepsData
	}

	if deletedHostsData := e.buildDeletedHostsData(decision); deletedHostsData != nil {
		explanationCtx.DeletedHosts = deletedHostsData
	}

	// Build step impacts
	if result := decision.Status.Result; result != nil && result.TargetHost != nil && len(result.StepResults) > 0 {
		targetHost := *result.TargetHost
		var inputWeights map[string]float64
		switch {
		case len(result.RawInWeights) > 0:
			inputWeights = result.RawInWeights
		case len(result.NormalizedInWeights) > 0:
			inputWeights = result.NormalizedInWeights
		}
		if inputWeights != nil {
			impacts := e.calculateStepImpacts(inputWeights, result.StepResults, targetHost)
			if len(impacts) > 0 {
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
				explanationCtx.StepImpacts = impacts
			}
		}
	}

	if chainData, err := e.buildChainData(ctx, decision); err != nil {
		return "", err
	} else if chainData != nil {
		explanationCtx.Chain = chainData
	}

	// Render using templates
	return e.templateManager.RenderExplanation(explanationCtx)
}

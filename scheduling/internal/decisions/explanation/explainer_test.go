// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestExplainer_Explain(t *testing.T) {
	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		historyDecisions []*v1alpha1.Decision
		expectedContains []string
		expectError      bool
	}{
		{
			name:             "initial nova server placement",
			decision:         WithResourceID(NewTestDecision("test-decision"), "test-resource-1"),
			expectedContains: []string{"Initial placement of the nova server"},
		},
		{
			name:             "initial cinder volume placement",
			decision:         WithDecisionType(WithResourceID(NewTestDecision("test-decision"), "test-resource-2"), v1alpha1.DecisionTypeCinderVolume),
			expectedContains: []string{"Initial placement of the cinder volume"},
		},
		{
			name:             "initial manila share placement",
			decision:         WithDecisionType(WithResourceID(NewTestDecision("test-decision"), "test-resource-3"), v1alpha1.DecisionTypeManilaShare),
			expectedContains: []string{"Initial placement of the manila share"},
		},
		{
			name:             "initial ironcore machine placement",
			decision:         WithDecisionType(WithResourceID(NewTestDecision("test-decision"), "test-resource-4"), v1alpha1.DecisionTypeIroncoreMachine),
			expectedContains: []string{"Initial placement of the ironcore machine"},
		},
		{
			name:             "unknown resource type falls back to generic",
			decision:         WithDecisionType(WithResourceID(NewTestDecision("test-decision"), "test-resource-5"), "unknown-type"),
			expectedContains: []string{"Initial placement of the resource"},
		},
		{
			name:             "empty history array",
			decision:         WithResourceID(NewTestDecision("test-decision"), "test-resource-6"),
			expectedContains: []string{"Initial placement of the nova server"},
		},
		{
			name: "subsequent decision with history",
			decision: WithHistoryRef(
				WithTargetHost(WithResourceID(NewTestDecision("test-decision-2"), "test-resource-7"), "host-2"),
				WithUID(WithTargetHost(WithResourceID(NewTestDecision("test-decision-1"), "test-resource-7"), "host-1"), "test-uid-1")),
			historyDecisions: []*v1alpha1.Decision{
				WithUID(WithTargetHost(WithResourceID(NewTestDecision("test-decision-1"), "test-resource-7"), "host-1"), "test-uid-1"),
			},
			expectedContains: []string{
				"Decision #2 for this nova server",
				"Previous target host was 'host-1'",
				"now it's 'host-2'",
			},
		},
		{
			name: "subsequent decision with nil target hosts",
			decision: WithHistoryRef(
				WithResourceID(NewTestDecision("test-decision-4"), "test-resource-8"),
				WithUID(WithResourceID(NewTestDecision("test-decision-3"), "test-resource-8"), "test-uid-3")),
			historyDecisions: []*v1alpha1.Decision{
				WithUID(WithResourceID(NewTestDecision("test-decision-3"), "test-resource-8"), "test-uid-3"),
			},
			expectedContains: []string{
				"Decision #2 for this nova server",
				"Previous target host was '(n/a)'",
				"now it's '(n/a)'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.historyDecisions) > 0 {
				RunExplanationTestWithHistory(t, tt.decision, tt.historyDecisions, tt.expectedContains)
			} else {
				RunExplanationTest(t, tt.decision, tt.expectedContains)
			}
		})
	}
}

func TestExplainer_Explain_HistoryDecisionNotFound(t *testing.T) {
	decision := NewDecision("test-decision").
		WithHistory([]corev1.ObjectReference{
			{
				Kind:      "Decision",
				Namespace: "default",
				Name:      "non-existent-decision",
				UID:       "non-existent-uid",
			},
		}).
		Build()

	explainer := SetupExplainerTest(t, decision)
	_, err := explainer.Explain(context.Background(), decision)
	if err == nil {
		t.Error("Expected error when previous decision not found, but got none")
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" || findInString(s, substr))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Generic Decision Helpers - Composable functions with smart defaults
func NewTestDecision(name string) *v1alpha1.Decision {
	return &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default", // Sensible default
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer, // Most common
			ResourceID: "test-resource",                 // Generic default
		},
		Status: v1alpha1.DecisionStatus{},
	}
}

func WithTargetHost(decision *v1alpha1.Decision, host string) *v1alpha1.Decision {
	if decision.Status.Result == nil {
		decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	decision.Status.Result.TargetHost = &host
	return decision
}

func WithInputWeights(decision *v1alpha1.Decision, weights map[string]float64) *v1alpha1.Decision {
	if decision.Status.Result == nil {
		decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	decision.Status.Result.RawInWeights = weights
	return decision
}

func WithOutputWeights(decision *v1alpha1.Decision, weights map[string]float64) *v1alpha1.Decision {
	if decision.Status.Result == nil {
		decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	decision.Status.Result.AggregatedOutWeights = weights

	// Auto-generate ordered hosts from weights
	hosts := make([]string, 0, len(weights))
	for host := range weights {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		return weights[hosts[i]] > weights[hosts[j]]
	})
	decision.Status.Result.OrderedHosts = hosts

	return decision
}

func WithSteps(decision *v1alpha1.Decision, steps ...v1alpha1.StepResult) *v1alpha1.Decision {
	if decision.Status.Result == nil {
		decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	decision.Status.Result.StepResults = steps
	return decision
}

func WithDecisionType(decision *v1alpha1.Decision, decisionType v1alpha1.DecisionType) *v1alpha1.Decision {
	decision.Spec.Type = decisionType
	return decision
}

func WithResourceID(decision *v1alpha1.Decision, resourceID string) *v1alpha1.Decision {
	decision.Spec.ResourceID = resourceID
	return decision
}

func WithUID(decision *v1alpha1.Decision, uid string) *v1alpha1.Decision {
	decision.UID = types.UID(uid)
	return decision
}

func WithHistory(decision *v1alpha1.Decision, refs []corev1.ObjectReference) *v1alpha1.Decision {
	decision.Status.History = &refs
	return decision
}

// Helper to create a decision with history reference to another decision
func WithHistoryRef(decision *v1alpha1.Decision, historyDecision *v1alpha1.Decision) *v1alpha1.Decision {
	refs := []corev1.ObjectReference{
		{
			Kind:      "Decision",
			Namespace: historyDecision.Namespace,
			Name:      historyDecision.Name,
			UID:       historyDecision.UID,
		},
	}
	decision.Status.History = &refs
	return decision
}

// Generic step creator
func Step(name string, activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: name},
		Activations: activations,
	}
}

// Common step names as constants
const (
	AvailabilityFilter = "availability-filter"
	ResourceWeigher    = "resource-weigher"
	PlacementPolicy    = "placement-policy"
)

// Decision Builder Pattern - Fluent interface for creating test decisions
type DecisionBuilder struct {
	decision *v1alpha1.Decision
}

func NewDecision(name string) *DecisionBuilder {
	return &DecisionBuilder{
		decision: &v1alpha1.Decision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: v1alpha1.DecisionSpec{
				Type:       v1alpha1.DecisionTypeNovaServer,
				ResourceID: "test-resource",
			},
			Status: v1alpha1.DecisionStatus{},
		},
	}
}

func (b *DecisionBuilder) WithResourceID(resourceID string) *DecisionBuilder {
	b.decision.Spec.ResourceID = resourceID
	return b
}

func (b *DecisionBuilder) WithType(decisionType v1alpha1.DecisionType) *DecisionBuilder {
	b.decision.Spec.Type = decisionType
	return b
}

func (b *DecisionBuilder) WithTargetHost(host string) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.TargetHost = stringPtr(host)
	return b
}

func (b *DecisionBuilder) WithRawInputWeights(weights map[string]float64) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.RawInWeights = weights
	return b
}

func (b *DecisionBuilder) WithNormalizedInputWeights(weights map[string]float64) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.NormalizedInWeights = weights
	return b
}

func (b *DecisionBuilder) WithAggregatedOutputWeights(weights map[string]float64) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.AggregatedOutWeights = weights
	return b
}

func (b *DecisionBuilder) WithOrderedHosts(hosts []string) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.OrderedHosts = hosts
	return b
}

func (b *DecisionBuilder) WithSteps(steps ...v1alpha1.StepResult) *DecisionBuilder {
	if b.decision.Status.Result == nil {
		b.decision.Status.Result = &v1alpha1.DecisionResult{}
	}
	b.decision.Status.Result.StepResults = steps
	return b
}

func (b *DecisionBuilder) WithHistory(refs []corev1.ObjectReference) *DecisionBuilder {
	b.decision.Status.History = &refs
	return b
}

func (b *DecisionBuilder) WithHistoryDecisions(decisions ...*v1alpha1.Decision) *DecisionBuilder {
	refs := make([]corev1.ObjectReference, len(decisions))
	for i, decision := range decisions {
		refs[i] = corev1.ObjectReference{
			Kind:      "Decision",
			Namespace: decision.Namespace,
			Name:      decision.Name,
			UID:       decision.UID,
		}
	}
	b.decision.Status.History = &refs
	return b
}

func (b *DecisionBuilder) WithPrecedence(precedence int) *DecisionBuilder {
	b.decision.Status.Precedence = intPtr(precedence)
	return b
}

func (b *DecisionBuilder) WithUID(uid string) *DecisionBuilder {
	b.decision.UID = types.UID(uid)
	return b
}

func (b *DecisionBuilder) WithCreationTimestamp(timestamp time.Time) *DecisionBuilder {
	b.decision.CreationTimestamp = metav1.Time{Time: timestamp}
	return b
}

func (b *DecisionBuilder) Build() *v1alpha1.Decision {
	return b.decision
}

// Pre-built scenario helpers for common test patterns
func DecisionWithScoring(name, winner string, scores map[string]float64) *DecisionBuilder {
	orderedHosts := make([]string, 0, len(scores))
	for host := range scores {
		orderedHosts = append(orderedHosts, host)
	}
	// Sort by score descending
	sort.Slice(orderedHosts, func(i, j int) bool {
		return scores[orderedHosts[i]] > scores[orderedHosts[j]]
	})

	return NewDecision(name).
		WithTargetHost(winner).
		WithAggregatedOutputWeights(scores).
		WithOrderedHosts(orderedHosts)
}

func DecisionWithInputComparison(name, winner string, inputWeights, finalWeights map[string]float64) *DecisionBuilder {
	return NewDecision(name).
		WithTargetHost(winner).
		WithRawInputWeights(inputWeights).
		WithAggregatedOutputWeights(finalWeights)
}

func DecisionWithCriticalSteps(name, winner string, inputWeights map[string]float64, steps ...v1alpha1.StepResult) *DecisionBuilder {
	return NewDecision(name).
		WithTargetHost(winner).
		WithRawInputWeights(inputWeights).
		WithSteps(steps...)
}

func DecisionWithHistory(name, winner string) *DecisionBuilder {
	return NewDecision(name).
		WithTargetHost(winner)
}

// Step result builders for common pipeline steps
func ResourceWeigherStep(activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: "resource-weigher"},
		Activations: activations,
	}
}

func AvailabilityFilterStep(activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: "availability-filter"},
		Activations: activations,
	}
}

func PlacementPolicyStep(activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: "placement-policy"},
		Activations: activations,
	}
}

func WeigherStep(name string, activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: name},
		Activations: activations,
	}
}

// Test execution helpers
func SetupExplainerTest(t *testing.T, decisions ...*v1alpha1.Decision) *Explainer {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	objects := make([]runtime.Object, len(decisions))
	for i, decision := range decisions {
		objects[i] = decision
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		Build()

	return &Explainer{Client: client}
}

func RunExplanationTest(t *testing.T, decision *v1alpha1.Decision, expectedContains []string) {
	explainer := SetupExplainerTest(t, decision)
	explanation, err := explainer.Explain(context.Background(), decision)
	AssertNoError(t, err)
	AssertExplanationContains(t, explanation, expectedContains...)
}

func RunExplanationTestWithHistory(t *testing.T, decision *v1alpha1.Decision, historyDecisions []*v1alpha1.Decision, expectedContains []string) {
	allDecisions := append(historyDecisions, decision)
	explainer := SetupExplainerTest(t, allDecisions...)
	explanation, err := explainer.Explain(context.Background(), decision)
	AssertNoError(t, err)
	AssertExplanationContains(t, explanation, expectedContains...)
}

func AssertNoError(t *testing.T, err error) {
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

func AssertExplanationContains(t *testing.T, explanation string, expected ...string) {
	for _, exp := range expected {
		if !contains(explanation, exp) {
			t.Errorf("Expected explanation to contain '%s', but got: %s", exp, explanation)
		}
	}
}

func AssertExplanationNotContains(t *testing.T, explanation string, notExpected ...string) {
	for _, notExp := range notExpected {
		if contains(explanation, notExp) {
			t.Errorf("Expected explanation to NOT contain '%s', but got: %s", notExp, explanation)
		}
	}
}

func TestExplainer_WinnerAnalysis(t *testing.T) {
	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "winner analysis with score gap",
			decision: DecisionWithScoring("test-decision", "host-1",
				map[string]float64{"host-1": 2.45, "host-2": 2.10, "host-3": 1.85}).
				Build(),
			expectedContains: []string{
				"Selected: host-1 (score: 2.45)",
				"gap to 2nd: 0.35",
				"3 hosts evaluated",
			},
		},
		{
			name: "winner analysis with single host",
			decision: DecisionWithScoring("test-decision", "host-1",
				map[string]float64{"host-1": 2.45}).
				Build(),
			expectedContains: []string{
				"Selected: host-1 (score: 2.45)",
				"1 hosts evaluated",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RunExplanationTest(t, tt.decision, tt.expectedContains)
		})
	}
}

func TestExplainer_InputComparison(t *testing.T) {
	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "input choice confirmed",
			decision: DecisionWithInputComparison("test-decision", "host-1",
				map[string]float64{"host-1": 1.20, "host-2": 1.10, "host-3": 0.95},
				map[string]float64{"host-1": 2.45, "host-2": 2.10, "host-3": 1.85}).
				Build(),
			expectedContains: []string{
				"Input choice confirmed: host-1 (1.20→2.45)",
			},
		},
		{
			name: "input choice overridden",
			decision: DecisionWithInputComparison("test-decision", "host-2",
				map[string]float64{"host-1": 1.50, "host-2": 1.20, "host-3": 0.95},
				map[string]float64{"host-1": 1.85, "host-2": 2.45, "host-3": 2.10}).
				Build(),
			expectedContains: []string{
				"Input favored host-1 (1.50), final winner: host-2 (1.20→2.45)",
			},
		},
		{
			name: "raw weights preferred over normalized",
			decision: NewDecision("test-decision").
				WithTargetHost("host-1").
				WithRawInputWeights(map[string]float64{"host-1": 100.0, "host-2": 90.0}).
				WithNormalizedInputWeights(map[string]float64{"host-1": 1.0, "host-2": 0.9}).
				WithAggregatedOutputWeights(map[string]float64{"host-1": 2.45, "host-2": 2.10}).
				Build(),
			expectedContains: []string{
				"Input choice confirmed: host-1 (100.00→2.45)", // Should now use raw weights (100.00)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RunExplanationTest(t, tt.decision, tt.expectedContains)
		})
	}
}

func TestExplainer_CriticalStepsAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "single critical step",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 2.0}),
				Step("resource-weigher", map[string]float64{"host-1": 1.5, "host-2": 0.2}),
				Step("availability-filter", map[string]float64{"host-1": 0.0, "host-2": 0.0})),
			expectedContains: []string{
				"Decision driven by 1/2 pipeline step: resource-weigher",
			},
		},
		{
			name: "multiple critical steps",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 3.0}),
				Step("resource-weigher", map[string]float64{"host-1": 1.0, "host-2": -0.5}),
				Step("availability-filter", map[string]float64{"host-1": 1.0, "host-2": 0.0}),
				Step("placement-policy", map[string]float64{"host-1": 0.05, "host-2": 0.05})),
			expectedContains: []string{
				"Decision driven by 2/3 pipeline steps: resource-weigher and availability-filter",
			},
		},
		{
			name: "all steps non-critical",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 3.0, "host-2": 1.0}),
				Step("step-1", map[string]float64{"host-1": 0.05, "host-2": 0.05}),
				Step("step-2", map[string]float64{"host-1": 0.02, "host-2": 0.02})),
			expectedContains: []string{
				"Decision driven by input only (all 2 steps are non-critical)",
			},
		},
		{
			name: "all steps critical",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 3.0}),
				Step("step-1", map[string]float64{"host-1": 1.0, "host-2": -0.5}),
				Step("step-2", map[string]float64{"host-1": 1.0, "host-2": 0.0})),
			expectedContains: []string{
				"Decision requires all 2 pipeline steps",
			},
		},
		{
			name: "three critical steps formatting",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 4.0}),
				Step("step-a", map[string]float64{"host-1": 1.0, "host-2": -0.5}),
				Step("step-b", map[string]float64{"host-1": 1.0, "host-2": 0.0}),
				Step("step-c", map[string]float64{"host-1": 1.0, "host-2": 0.0}),
				Step("step-d", map[string]float64{"host-1": 0.05, "host-2": 0.05})),
			expectedContains: []string{
				"Decision driven by 3/4 pipeline steps: step-a, step-b and step-c",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

func TestExplainer_CompleteExplanation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Test a complete explanation with all features
	previousDecision := WithUID(WithTargetHost(NewTestDecision("test-decision-1"), "host-1"), "test-uid-1")

	decision := WithSteps(
		WithOutputWeights(
			WithInputWeights(
				WithHistoryRef(
					WithTargetHost(NewTestDecision("test-decision-2"), "host-2"),
					previousDecision),
				map[string]float64{"host-1": 1.50, "host-2": 1.20, "host-3": 0.95}),
			map[string]float64{"host-1": 1.85, "host-2": 2.45, "host-3": 2.10}),
		Step("resource-weigher", map[string]float64{"host-1": 0.15, "host-2": 0.85, "host-3": 0.75}),
		Step("availability-filter", map[string]float64{"host-1": 0.20, "host-2": 0.40, "host-3": 0.40}))

	// Set precedence manually since it's not commonly used
	decision.Status.Precedence = intPtr(1)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision, previousDecision).
		Build()

	explainer := &Explainer{Client: client}

	explanation, err := explainer.Explain(context.Background(), decision)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
		return
	}

	expectedParts := []string{
		"Decision #2 for this nova server",
		"Previous target host was 'host-1', now it's 'host-2'",
		"Selected: host-2 (score: 2.45), gap to 2nd: 0.35, 3 hosts evaluated",
		"Input favored host-1 (1.50), final winner: host-2 (1.20→2.45)",
		"Decision driven by 1/2 pipeline step: resource-weigher",
	}

	for _, expected := range expectedParts {
		if !contains(explanation, expected) {
			t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
		}
	}
}

func TestExplainer_DeletedHostsAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "single host filtered by single step",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 2.0}),
				Step("availability-filter", map[string]float64{"host-1": 0.5})),
			expectedContains: []string{
				"Input winner host-2 was filtered by availability-filter",
			},
		},
		{
			name: "multiple hosts filtered",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 3.0, "host-2": 2.0, "host-3": 1.0}),
				Step("availability-filter", map[string]float64{"host-1": 0.5})),
			expectedContains: []string{
				"2 hosts filtered",
			},
		},
		{
			name: "multiple hosts filtered including input winner",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 3.0, "host-3": 2.0}),
				Step("availability-filter", map[string]float64{"host-1": 0.5})),
			expectedContains: []string{
				"2 hosts filtered (including input winner host-2)",
			},
		},
		{
			name: "no hosts filtered",
			decision: WithSteps(
				WithInputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 1.0, "host-2": 2.0}),
				Step("resource-weigher", map[string]float64{"host-1": 0.5, "host-2": 0.3})),
			expectedContains: []string{}, // No deleted hosts analysis should be present
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}

			// For the "no hosts filtered" case, ensure no deleted hosts analysis is present
			if len(tt.expectedContains) == 0 {
				deletedHostsKeywords := []string{"filtered", "Input winner", "hosts filtered"}
				for _, keyword := range deletedHostsKeywords {
					if contains(explanation, keyword) {
						t.Errorf("Expected explanation to NOT contain '%s' for no deleted hosts case, but got: %s", keyword, explanation)
					}
				}
			}
		})
	}
}

func TestExplainer_GlobalChainAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create timestamps for testing durations
	baseTime := metav1.Now()
	time1 := metav1.Time{Time: baseTime.Add(-120 * time.Minute)} // 2 hours ago
	time2 := metav1.Time{Time: baseTime.Add(-60 * time.Minute)}  // 1 hour ago
	time3 := metav1.Time{Time: baseTime.Time}                    // now

	tests := []struct {
		name               string
		currentDecision    *v1alpha1.Decision
		historyDecisions   []v1alpha1.Decision
		expectedContains   []string
		expectedNotContain []string
	}{
		{
			name: "simple chain with durations",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-3",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-3"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-2"),
						},
					},
				},
			},
			expectedContains: []string{
				"Chain: host-1 (1h) -> host-2 (1h) -> host-3 (0s).",
			},
		},
		{
			name: "chain with loop detection",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-3",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"), // Back to host-1 - creates loop
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-2"),
						},
					},
				},
			},
			expectedContains: []string{
				"Chain (loop detected): host-1 (1h) -> host-2 (1h) -> host-1 (0s).",
			},
		},
		{
			name: "chain with multiple decisions on same host",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-4",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
						{Kind: "Decision", Namespace: "default", Name: "decision-3", UID: "uid-3"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-2"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time1, // Same time as decision-1
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"), // Same host as decision-1
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-3",
						Namespace:         "default",
						UID:               "uid-3",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"), // Still same host
						},
					},
				},
			},
			expectedContains: []string{
				"Chain: host-1 (2h; 3 decisions) -> host-2 (0s).",
			},
		},
		{
			name: "no chain for initial decision",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "decision-1",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil, // No history
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{},
			expectedNotContain: []string{
				"Chain:",
				"chain:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.currentDecision}
			for i := range tt.historyDecisions {
				objects = append(objects, &tt.historyDecisions[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.currentDecision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}

			for _, notExpected := range tt.expectedNotContain {
				if contains(explanation, notExpected) {
					t.Errorf("Expected explanation to NOT contain '%s', but got: %s", notExpected, explanation)
				}
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

// TestExplainer_RawWeightsPriorityBugFix tests that the explainer correctly prioritizes
// raw weights over normalized weights to preserve small but important differences.
// This test verifies the fix for the bug where normalized weights were incorrectly preferred.
func TestExplainer_RawWeightsPriorityBugFix(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
		description      string
	}{
		{
			name: "raw_weights_preserve_small_differences",
			decision: func() *v1alpha1.Decision {
				decision := WithOutputWeights(
					WithInputWeights(
						WithTargetHost(NewTestDecision("test-decision"), "host-2"),
						map[string]float64{"host-1": 1000.05, "host-2": 1000.10, "host-3": 1000.00}),
					map[string]float64{"host-1": 1001.05, "host-2": 1002.10, "host-3": 1001.00})
				// Add normalized weights to show they would mask the difference
				decision.Status.Result.NormalizedInWeights = map[string]float64{"host-1": 1.0, "host-2": 1.0, "host-3": 1.0}
				return decision
			}(),
			expectedContains: []string{
				"Input choice confirmed: host-2 (1000.10→1002.10)", // Should use raw weights (1000.10)
			},
			description: "Raw weights preserve small differences that normalized weights would mask",
		},
		{
			name: "raw_weights_detect_correct_input_winner",
			decision: func() *v1alpha1.Decision {
				decision := WithOutputWeights(
					WithInputWeights(
						WithTargetHost(NewTestDecision("test-decision"), "host-3"),
						map[string]float64{"host-1": 2000.15, "host-2": 2000.10, "host-3": 2000.05}),
					map[string]float64{"host-1": 2001.15, "host-2": 2001.10, "host-3": 2002.05})
				// Add normalized weights to show they would mask the difference
				decision.Status.Result.NormalizedInWeights = map[string]float64{"host-1": 1.0, "host-2": 1.0, "host-3": 1.0}
				return decision
			}(),
			expectedContains: []string{
				"Input favored host-1 (2000.15), final winner: host-3 (2000.05→2002.05)", // Should detect host-1 as input winner using raw weights
			},
			description: "Raw weights correctly identify input winner that normalized weights would miss",
		},
		{
			name: "critical_steps_analysis_uses_raw_weights",
			decision: func() *v1alpha1.Decision {
				decision := WithSteps(
					WithInputWeights(
						WithTargetHost(NewTestDecision("test-decision"), "host-1"),
						map[string]float64{"host-1": 1000.05, "host-2": 1000.00}),
					Step("resource-weigher", map[string]float64{"host-1": 0.5, "host-2": 0.0}))
				// Add normalized weights to show they would mask the difference
				decision.Status.Result.NormalizedInWeights = map[string]float64{"host-1": 1.0, "host-2": 1.0}
				return decision
			}(),
			expectedContains: []string{
				"Decision driven by input only (all 1 steps are non-critical)", // With small raw weight advantage, step is non-critical
				"Input choice confirmed: host-1 (1000.05→0.00)",                // Shows raw weights are being used
			},
			description: "Critical steps analysis uses raw weights - with small raw advantage, step becomes non-critical",
		},
		{
			name: "deleted_hosts_analysis_uses_raw_weights",
			decision: func() *v1alpha1.Decision {
				decision := WithSteps(
					WithInputWeights(
						WithTargetHost(NewTestDecision("test-decision"), "host-1"),
						map[string]float64{"host-1": 1000.00, "host-2": 1000.05, "host-3": 999.95}),
					Step("availability-filter", map[string]float64{"host-1": 0.0}))
				// Add normalized weights to show they would mask the difference
				decision.Status.Result.NormalizedInWeights = map[string]float64{"host-1": 1.0, "host-2": 1.0, "host-3": 1.0}
				return decision
			}(),
			expectedContains: []string{
				"2 hosts filtered (including input winner host-2)",                    // Shows raw weights are used to identify input winner
				"Input favored host-2 (1000.05), final winner: host-1 (1000.00→0.00)", // Shows raw weights in input comparison
			},
			description: "Deleted hosts analysis uses raw weights to correctly identify input winner",
		},
		{
			name: "fallback_to_normalized_when_no_raw_weights",
			decision: func() *v1alpha1.Decision {
				decision := WithOutputWeights(
					WithTargetHost(NewTestDecision("test-decision"), "host-1"),
					map[string]float64{"host-1": 2.5, "host-2": 2.0, "host-3": 1.8})
				// Set normalized weights and clear raw weights to test fallback
				decision.Status.Result.NormalizedInWeights = map[string]float64{"host-1": 1.5, "host-2": 1.0, "host-3": 0.8}
				decision.Status.Result.RawInWeights = nil
				return decision
			}(),
			expectedContains: []string{
				"Input choice confirmed: host-1 (1.50→2.50)", // Should use normalized weights as fallback
			},
			description: "Should fall back to normalized weights when raw weights are not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

// TestExplainer_RawVsNormalizedComparison demonstrates the impact of the bug fix
func TestExplainer_RawVsNormalizedComparison(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// This test demonstrates what would happen with the old (buggy) behavior
	// vs the new (correct) behavior
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer,
			ResourceID: "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			Result: &v1alpha1.DecisionResult{
				TargetHost: stringPtr("host-2"),
				RawInWeights: map[string]float64{
					"host-1": 1000.05, // Very small difference
					"host-2": 1000.10, // Slightly higher - should be detected as input winner
					"host-3": 1000.00,
				},
				NormalizedInWeights: map[string]float64{
					"host-1": 1.0, // All normalized to same value - would mask the difference
					"host-2": 1.0,
					"host-3": 1.0,
				},
				AggregatedOutWeights: map[string]float64{
					"host-1": 1001.05,
					"host-2": 1002.10, // host-2 wins
					"host-3": 1001.00,
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision).
		Build()

	explainer := &Explainer{Client: client}
	explanation, err := explainer.Explain(context.Background(), decision)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
		return
	}

	// With the fix, should correctly identify host-2 as input winner using raw weights
	if !contains(explanation, "Input choice confirmed: host-2 (1000.10→1002.10)") {
		t.Errorf("Expected explanation to show raw weight value (1000.10), but got: %s", explanation)
	}

	// Should NOT show any indication that input choice was overridden
	if contains(explanation, "Input favored host-1") || contains(explanation, "Input favored host-3") {
		t.Errorf("Expected explanation to NOT show input choice override, but got: %s", explanation)
	}

}

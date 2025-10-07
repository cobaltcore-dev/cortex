// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	decisionsv1alpha1 "github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

// Test constants to reduce magic numbers
const (
	DefaultTestTTL   = 2 * time.Hour
	DefaultTestAge   = 1 * time.Hour
	OldTestAge       = 3 * time.Hour
	TestTolerance    = 1 * time.Minute
	DefaultTestVCPUs = 1
	DefaultTestRAM   = 2048
	DefaultTestDisk  = 10
)

// TestDecisionBuilder helps build SchedulingDecisionRequest objects for tests
type TestDecisionBuilder struct {
	decision decisionsv1alpha1.SchedulingDecisionRequest
}

func NewTestDecision(id string) *TestDecisionBuilder {
	return &TestDecisionBuilder{
		decision: decisionsv1alpha1.SchedulingDecisionRequest{
			ID:          id,
			RequestedAt: metav1.NewTime(time.Now()),
			EventType:   decisionsv1alpha1.SchedulingEventTypeInitialPlacement,
			Input: map[string]float64{
				"host1": 1.0,
			},
			Pipeline: decisionsv1alpha1.SchedulingDecisionPipelineSpec{
				Name: "test-pipeline",
			},
			Flavor: decisionsv1alpha1.Flavor{
				Name: "test-flavor",
				Resources: map[string]resource.Quantity{
					"cpu":     *resource.NewQuantity(int64(DefaultTestVCPUs), resource.DecimalSI),
					"memory":  *resource.NewQuantity(int64(DefaultTestRAM), resource.DecimalSI),
					"storage": *resource.NewQuantity(int64(DefaultTestDisk), resource.DecimalSI),
				},
			},
		},
	}
}

// WithRequestedAt sets the RequestedAt timestamp
func (b *TestDecisionBuilder) WithRequestedAt(t time.Time) *TestDecisionBuilder {
	b.decision.RequestedAt = metav1.NewTime(t)
	return b
}

// WithInput sets the input hosts and scores
func (b *TestDecisionBuilder) WithInput(input map[string]float64) *TestDecisionBuilder {
	b.decision.Input = input
	return b
}

// WithPipelineOutputs sets the pipeline outputs
func (b *TestDecisionBuilder) WithPipelineOutputs(outputs ...decisionsv1alpha1.SchedulingDecisionPipelineOutputSpec) *TestDecisionBuilder {
	b.decision.Pipeline.Outputs = outputs
	return b
}

// WithEventType sets the event type
func (b *TestDecisionBuilder) WithEventType(eventType decisionsv1alpha1.SchedulingEventType) *TestDecisionBuilder {
	b.decision.EventType = eventType
	return b
}

// Build returns the built SchedulingDecisionRequest
func (b *TestDecisionBuilder) Build() decisionsv1alpha1.SchedulingDecisionRequest {
	return b.decision
}

// TestSchedulingDecisionBuilder helps build SchedulingDecision objects for tests
type TestSchedulingDecisionBuilder struct {
	resource decisionsv1alpha1.SchedulingDecision
}

// NewTestSchedulingDecision creates a new test SchedulingDecision builder
func NewTestSchedulingDecision(name string) *TestSchedulingDecisionBuilder {
	return &TestSchedulingDecisionBuilder{
		resource: decisionsv1alpha1.SchedulingDecision{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: decisionsv1alpha1.SchedulingDecisionSpec{
				Decisions: []decisionsv1alpha1.SchedulingDecisionRequest{},
			},
		},
	}
}

// WithDecisions adds decisions to the SchedulingDecision
func (b *TestSchedulingDecisionBuilder) WithDecisions(decisions ...decisionsv1alpha1.SchedulingDecisionRequest) *TestSchedulingDecisionBuilder {
	b.resource.Spec.Decisions = decisions
	return b
}

// WithCreationTimestamp sets the creation timestamp
func (b *TestSchedulingDecisionBuilder) WithCreationTimestamp(t time.Time) *TestSchedulingDecisionBuilder {
	b.resource.ObjectMeta.CreationTimestamp = metav1.NewTime(t)
	return b
}

// WithNamespace sets the namespace
func (b *TestSchedulingDecisionBuilder) WithNamespace(namespace string) *TestSchedulingDecisionBuilder {
	b.resource.ObjectMeta.Namespace = namespace
	return b
}

// Build returns the built SchedulingDecision
func (b *TestSchedulingDecisionBuilder) Build() *decisionsv1alpha1.SchedulingDecision {
	return &b.resource
}

// NewTestPipelineOutput creates a pipeline output spec for testing
func NewTestPipelineOutput(step string, activations map[string]float64) decisionsv1alpha1.SchedulingDecisionPipelineOutputSpec {
	return decisionsv1alpha1.SchedulingDecisionPipelineOutputSpec{
		Step:        step,
		Activations: activations,
	}
}

// SetupTestEnvironment creates a fake client and scheme for testing
func SetupTestEnvironment(t *testing.T, resources ...client.Object) (client.Client, *runtime.Scheme) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := decisionsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
	if len(resources) > 0 {
		clientBuilder = clientBuilder.WithObjects(resources...)
	}

	// Add status subresource for SchedulingDecision
	fakeClient := clientBuilder.WithStatusSubresource(&decisionsv1alpha1.SchedulingDecision{}).Build()

	return fakeClient, scheme
}

// CreateTestRequest creates a controller request for testing
func CreateTestRequest(name string, namespace ...string) ctrl.Request {
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name: name,
		},
	}
	if len(namespace) > 0 {
		req.NamespacedName.Namespace = namespace[0]
	}
	return req
}

// AssertResourceExists checks that a resource exists and returns it
func AssertResourceExists(t *testing.T, c client.Client, name string, namespace ...string) *decisionsv1alpha1.SchedulingDecision {
	t.Helper()

	key := client.ObjectKey{Name: name}
	if len(namespace) > 0 {
		key.Namespace = namespace[0]
	}

	var resource decisionsv1alpha1.SchedulingDecision
	if err := c.Get(t.Context(), key, &resource); err != nil {
		t.Fatalf("Resource %s should exist: %v", name, err)
	}
	return &resource
}

// AssertResourceDeleted checks that a resource has been deleted
func AssertResourceDeleted(t *testing.T, c client.Client, name string, namespace ...string) {
	t.Helper()

	key := client.ObjectKey{Name: name}
	if len(namespace) > 0 {
		key.Namespace = namespace[0]
	}

	var resource decisionsv1alpha1.SchedulingDecision
	err := c.Get(t.Context(), key, &resource)
	if err == nil {
		t.Errorf("Resource %s should have been deleted", name)
	}
}

// AssertResourceState checks the state of a SchedulingDecision
func AssertResourceState(t *testing.T, resource *decisionsv1alpha1.SchedulingDecision, expectedState decisionsv1alpha1.SchedulingDecisionState) {
	t.Helper()

	if resource.Status.State != expectedState {
		t.Errorf("Expected state '%s', got '%s'", expectedState, resource.Status.State)
	}
}

// AssertResourceError checks the error message of a SchedulingDecision
func AssertResourceError(t *testing.T, resource *decisionsv1alpha1.SchedulingDecision, expectedError string) {
	t.Helper()

	if resource.Status.Error != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, resource.Status.Error)
	}
}

// AssertNoError checks that there's no error in the resource status
func AssertNoError(t *testing.T, resource *decisionsv1alpha1.SchedulingDecision) {
	t.Helper()

	if resource.Status.Error != "" {
		t.Errorf("Expected no error, got '%s'", resource.Status.Error)
	}
}

// AssertResultCount checks the number of results in a SchedulingDecision
func AssertResultCount(t *testing.T, resource *decisionsv1alpha1.SchedulingDecision, expectedCount int) {
	t.Helper()

	if len(resource.Status.Results) != expectedCount {
		t.Errorf("Expected %d results, got %d", expectedCount, len(resource.Status.Results))
	}
}

// AssertDecisionCount checks the decision count in a SchedulingDecision
func AssertDecisionCount(t *testing.T, resource *decisionsv1alpha1.SchedulingDecision, expectedCount int) {
	t.Helper()

	if resource.Status.DecisionCount != expectedCount {
		t.Errorf("Expected decision count %d, got %d", expectedCount, resource.Status.DecisionCount)
	}
}

// AssertFinalScores checks the final scores in a result
func AssertFinalScores(t *testing.T, result decisionsv1alpha1.SchedulingDecisionResult, expectedScores map[string]float64) {
	t.Helper()

	if len(result.FinalScores) != len(expectedScores) {
		t.Errorf("Expected %d final scores, got %d", len(expectedScores), len(result.FinalScores))
	}

	for host, expectedScore := range expectedScores {
		if actualScore, exists := result.FinalScores[host]; !exists {
			t.Errorf("Expected final score for host '%s', but it was not found", host)
		} else if actualScore != expectedScore {
			t.Errorf("Expected final score for host '%s' to be %f, got %f", host, expectedScore, actualScore)
		}
	}
}

// AssertDeletedHosts checks the deleted hosts in a result
func AssertDeletedHosts(t *testing.T, result decisionsv1alpha1.SchedulingDecisionResult, expectedDeletedHosts map[string][]string) {
	t.Helper()

	if len(result.DeletedHosts) != len(expectedDeletedHosts) {
		t.Errorf("Expected %d deleted hosts, got %d", len(expectedDeletedHosts), len(result.DeletedHosts))
	}

	for host, expectedSteps := range expectedDeletedHosts {
		if actualSteps, exists := result.DeletedHosts[host]; !exists {
			t.Errorf("Expected deleted host '%s', but it was not found", host)
		} else if len(actualSteps) != len(expectedSteps) {
			t.Errorf("Expected host '%s' to be deleted by %d steps, got %d", host, len(expectedSteps), len(actualSteps))
		} else {
			for i, expectedStep := range expectedSteps {
				if actualSteps[i] != expectedStep {
					t.Errorf("Expected host '%s' step %d to be '%s', got '%s'", host, i, expectedStep, actualSteps[i])
				}
			}
		}
	}
}

// AssertDescriptionContains checks that a description contains expected text
func AssertDescriptionContains(t *testing.T, description string, expectedContents ...string) {
	t.Helper()

	for _, expectedContent := range expectedContents {
		if !strings.Contains(description, expectedContent) {
			t.Errorf("Expected description to contain '%s', got '%s'", expectedContent, description)
		}
	}
}

// CreateTTLReconciler creates a TTL reconciler with the given TTL duration
// If ttl is 0, the reconciler will use its internal default
func CreateTTLReconciler(fakeClient client.Client, scheme *runtime.Scheme, ttl time.Duration) *SchedulingDecisionTTLController {
	return &SchedulingDecisionTTLController{
		Client: fakeClient,
		Scheme: scheme,
		Conf: Config{
			TTLAfterDecision: ttl,
		},
	}
}

// CreateSchedulingReconciler creates a scheduling decision reconciler
// If conf is empty, uses default empty config
func CreateSchedulingReconciler(fakeClient client.Client, conf ...Config) *SchedulingDecisionReconciler {
	var config Config
	if len(conf) > 0 {
		config = conf[0]
	}
	return &SchedulingDecisionReconciler{
		Conf:   config,
		Client: fakeClient,
	}
}

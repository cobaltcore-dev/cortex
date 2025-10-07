// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"
	"time"
)

func TestTTLController(t *testing.T) {
	tests := []struct {
		name          string
		resourceAge   time.Duration
		ttl           time.Duration
		expectDeleted bool
		expectRequeue bool
	}{
		{
			name:          "young resource preserved",
			resourceAge:   DefaultTestAge,
			ttl:           DefaultTestTTL,
			expectDeleted: false,
			expectRequeue: true,
		},
		{
			name:          "old resource deleted",
			resourceAge:   OldTestAge,
			ttl:           DefaultTestTTL,
			expectDeleted: true,
			expectRequeue: false,
		},
		{
			name:          "resource at TTL boundary deleted",
			resourceAge:   DefaultTestTTL,
			ttl:           DefaultTestTTL,
			expectDeleted: true,
			expectRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test resource with specified age
			decision := NewTestDecision("decision-1").
				WithRequestedAt(time.Now().Add(-tt.resourceAge)).
				Build()

			resource := NewTestSchedulingDecision("test-decision").
				WithDecisions(decision).
				Build()

			fakeClient, scheme := SetupTestEnvironment(t, resource)
			reconciler := CreateTTLReconciler(fakeClient, scheme, tt.ttl)
			req := CreateTestRequest("test-decision")

			result, err := reconciler.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile failed: %v", err)
			}

			// Check deletion expectation
			if tt.expectDeleted {
				AssertResourceDeleted(t, fakeClient, "test-decision")
			} else {
				AssertResourceExists(t, fakeClient, "test-decision")
			}

			// Check requeue expectation
			if tt.expectRequeue && result.RequeueAfter == 0 {
				t.Error("Expected requeue but got none")
			}
			if !tt.expectRequeue && result.RequeueAfter != 0 {
				t.Error("Expected no requeue but got one")
			}
		})
	}
}

func TestTTLControllerFallbackToCreationTimestamp(t *testing.T) {
	// Resource with no decisions should use creation timestamp
	resource := NewTestSchedulingDecision("empty-decision").
		WithCreationTimestamp(time.Now().Add(-OldTestAge)).
		Build()

	fakeClient, scheme := SetupTestEnvironment(t, resource)
	reconciler := CreateTTLReconciler(fakeClient, scheme, DefaultTestTTL)
	req := CreateTestRequest("empty-decision")

	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Should be deleted and not requeued
	AssertResourceDeleted(t, fakeClient, "empty-decision")
	if result.RequeueAfter != 0 {
		t.Error("Expected no requeue after deletion")
	}
}

func TestTTLControllerDefaultTTL(t *testing.T) {
	decision := NewTestDecision("decision-1").
		WithRequestedAt(time.Now().Add(-DefaultTestAge)).
		Build()

	resource := NewTestSchedulingDecision("default-ttl-decision").
		WithDecisions(decision).
		Build()

	fakeClient, scheme := SetupTestEnvironment(t, resource)

	// Create reconciler without TTL config (should use default)
	reconciler := CreateTTLReconciler(fakeClient, scheme, 0) // Zero duration means use default

	req := CreateTestRequest("default-ttl-decision")
	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// 1-hour-old resource with default TTL should be preserved
	AssertResourceExists(t, fakeClient, "default-ttl-decision")
	if result.RequeueAfter == 0 {
		t.Error("Expected requeue for resource with default TTL")
	}

	// Verify requeue time is reasonable
	expectedRequeue := time.Duration(DefaultTTLAfterDecisionSeconds)*time.Second - DefaultTestAge
	if result.RequeueAfter < expectedRequeue-TestTolerance || result.RequeueAfter > expectedRequeue+TestTolerance {
		t.Errorf("Requeue time %v not within expected range %v ± %v",
			result.RequeueAfter, expectedRequeue, TestTolerance)
	}
}

func TestTTLControllerNonExistentResource(t *testing.T) {
	fakeClient, scheme := SetupTestEnvironment(t)
	reconciler := CreateTTLReconciler(fakeClient, scheme, DefaultTestTTL)
	req := CreateTestRequest("non-existent")

	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Should handle non-existent resources gracefully: %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Error("Expected no requeue for non-existent resource")
	}
}

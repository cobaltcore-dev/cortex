// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestBasePipelineController_UpdateDecision_HistoryLimiting(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name                    string
		maxHistoryEntries       int
		existingHistoryCount    int
		expectedHistoryCount    int
		expectedOldestPreserved bool // Whether the oldest entry should still be present
	}{
		{
			name:                    "unlimited history (maxHistoryEntries=0)",
			maxHistoryEntries:       0,
			existingHistoryCount:    15,
			expectedHistoryCount:    16, // 15 existing + 1 new
			expectedOldestPreserved: true,
		},
		{
			name:                    "history within limit",
			maxHistoryEntries:       10,
			existingHistoryCount:    5,
			expectedHistoryCount:    6, // 5 existing + 1 new
			expectedOldestPreserved: true,
		},
		{
			name:                    "history exactly at limit",
			maxHistoryEntries:       10,
			existingHistoryCount:    10,
			expectedHistoryCount:    10, // oldest removed, new added
			expectedOldestPreserved: false,
		},
		{
			name:                    "history exceeds limit",
			maxHistoryEntries:       5,
			existingHistoryCount:    10,
			expectedHistoryCount:    5, // trimmed to 5
			expectedOldestPreserved: false,
		},
		{
			name:                    "small history limit",
			maxHistoryEntries:       1,
			existingHistoryCount:    3,
			expectedHistoryCount:    1, // only the newest
			expectedOldestPreserved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create existing history entries
			existingHistory := make([]v1alpha1.SchedulingHistoryEntry, tt.existingHistoryCount)
			for i := range tt.existingHistoryCount {
				existingHistory[i] = v1alpha1.SchedulingHistoryEntry{
					OrderedHosts: []string{"host-" + string(rune('a'+i))},
					Timestamp:    metav1.Time{Time: time.Now().Add(-time.Hour * time.Duration(tt.existingHistoryCount-i))},
					PipelineRef: corev1.ObjectReference{
						APIVersion: "cortex.cloud/v1alpha1",
						Kind:       "Pipeline",
						Name:       "test-pipeline",
					},
					Intent: v1alpha1.SchedulingIntentUnknown,
				}
			}

			// Create decision with existing history
			decision := &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-resource-id",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-resource-id",
				},
				Status: v1alpha1.DecisionStatus{
					SchedulingHistory: existingHistory,
				},
			}

			// Create pipeline config with max history setting
			pipelineConfig := &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain:  v1alpha1.SchedulingDomainNova,
					Type:              v1alpha1.PipelineTypeFilterWeigher,
					MaxHistoryEntries: tt.maxHistoryEntries,
				},
			}

			// Setup fake client with decision and pipeline
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(decision, pipelineConfig).
				WithStatusSubresource(decision).
				Build()

			// Create controller
			controller := &BasePipelineController[any]{
				Client:           fakeClient,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				PipelineConfigs: map[string]v1alpha1.Pipeline{
					"test-pipeline": *pipelineConfig,
				},
			}

			// Create decision update
			update := DecisionUpdate{
				ResourceID:   "test-resource-id",
				PipelineName: "test-pipeline",
				Result: FilterWeigherPipelineResult{
					OrderedHosts: []string{"new-host"},
				},
				Intent: v1alpha1.SchedulingIntentUnknown,
			}

			// Perform the update
			err := controller.updateDecision(context.Background(), update)
			if err != nil {
				t.Fatalf("updateDecision failed: %v", err)
			}

			// Retrieve updated decision
			var updatedDecision v1alpha1.Decision
			err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-resource-id"}, &updatedDecision)
			if err != nil {
				t.Fatalf("Failed to get updated decision: %v", err)
			}

			// Check history count
			actualCount := len(updatedDecision.Status.SchedulingHistory)
			if actualCount != tt.expectedHistoryCount {
				t.Errorf("Expected %d history entries, got %d", tt.expectedHistoryCount, actualCount)
			}

			// Check that newest entry is present
			if actualCount > 0 {
				newestEntry := updatedDecision.Status.SchedulingHistory[actualCount-1]
				if len(newestEntry.OrderedHosts) != 1 || newestEntry.OrderedHosts[0] != "new-host" {
					t.Errorf("Expected newest entry to have host 'new-host', got %v", newestEntry.OrderedHosts)
				}
				if newestEntry.Intent != v1alpha1.SchedulingIntentUnknown {
					t.Errorf("Expected newest entry intent to be Unknown, got %v", newestEntry.Intent)
				}
			}

			// Check if oldest entry was preserved (when expected)
			if tt.existingHistoryCount > 0 && actualCount > 0 {
				oldestInResult := updatedDecision.Status.SchedulingHistory[0]
				oldestOriginal := existingHistory[0]
				isOldestPreserved := oldestInResult.OrderedHosts[0] == oldestOriginal.OrderedHosts[0]

				if isOldestPreserved != tt.expectedOldestPreserved {
					t.Errorf("Expected oldest preserved: %v, got: %v", tt.expectedOldestPreserved, isOldestPreserved)
				}
			}
		})
	}
}

func TestBasePipelineController_UpdateDecision_PipelineRef(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-resource-id",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			ResourceID:       "test-resource-id",
		},
	}

	pipelineConfig := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain:  v1alpha1.SchedulingDomainNova,
			Type:              v1alpha1.PipelineTypeFilterWeigher,
			MaxHistoryEntries: 10,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(decision, pipelineConfig).
		WithStatusSubresource(decision).
		Build()

	controller := &BasePipelineController[any]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		PipelineConfigs: map[string]v1alpha1.Pipeline{
			"test-pipeline": *pipelineConfig,
		},
	}

	update := DecisionUpdate{
		ResourceID:   "test-resource-id",
		PipelineName: "test-pipeline",
		Result: FilterWeigherPipelineResult{
			OrderedHosts: []string{"host-a"},
		},
		Intent: v1alpha1.SchedulingIntentUnknown,
	}

	err := controller.updateDecision(context.Background(), update)
	if err != nil {
		t.Fatalf("updateDecision failed: %v", err)
	}

	// Retrieve updated decision
	var updatedDecision v1alpha1.Decision
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-resource-id"}, &updatedDecision)
	if err != nil {
		t.Fatalf("Failed to get updated decision: %v", err)
	}

	// Verify PipelineRef has all required fields
	if len(updatedDecision.Status.SchedulingHistory) == 0 {
		t.Fatal("Expected at least one history entry")
	}

	pipelineRef := updatedDecision.Status.SchedulingHistory[0].PipelineRef

	if pipelineRef.APIVersion != "cortex.cloud/v1alpha1" {
		t.Errorf("Expected APIVersion 'cortex.cloud/v1alpha1', got %q", pipelineRef.APIVersion)
	}
	if pipelineRef.Kind != "Pipeline" {
		t.Errorf("Expected Kind 'Pipeline', got %q", pipelineRef.Kind)
	}
	if pipelineRef.Name != "test-pipeline" {
		t.Errorf("Expected Name 'test-pipeline', got %q", pipelineRef.Name)
	}
}

func TestBasePipelineController_UpdateDecision_CreateIfNotExists(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	pipelineConfig := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain:  v1alpha1.SchedulingDomainCinder,
			Type:              v1alpha1.PipelineTypeFilterWeigher,
			MaxHistoryEntries: 10,
		},
	}

	// Don't create the decision - it should be created by updateDecision
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipelineConfig).
		WithStatusSubresource(&v1alpha1.Decision{}).
		Build()

	controller := &BasePipelineController[any]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainCinder,
		PipelineConfigs: map[string]v1alpha1.Pipeline{
			"test-pipeline": *pipelineConfig,
		},
	}

	update := DecisionUpdate{
		ResourceID:   "new-resource-id",
		PipelineName: "test-pipeline",
		Result: FilterWeigherPipelineResult{
			OrderedHosts: []string{"host-a", "host-b"},
		},
		Intent: v1alpha1.SchedulingIntentUnknown,
	}

	err := controller.updateDecision(context.Background(), update)
	if err != nil {
		t.Fatalf("updateDecision failed: %v", err)
	}

	// Verify decision was created
	var createdDecision v1alpha1.Decision
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "new-resource-id"}, &createdDecision)
	if err != nil {
		t.Fatalf("Failed to get created decision: %v", err)
	}

	// Verify spec fields
	if createdDecision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainCinder {
		t.Errorf("Expected scheduling domain 'cinder', got %v", createdDecision.Spec.SchedulingDomain)
	}
	if createdDecision.Spec.ResourceID != "new-resource-id" {
		t.Errorf("Expected resource ID 'new-resource-id', got %v", createdDecision.Spec.ResourceID)
	}

	// Verify history entry
	if len(createdDecision.Status.SchedulingHistory) != 1 {
		t.Fatalf("Expected 1 history entry, got %d", len(createdDecision.Status.SchedulingHistory))
	}

	entry := createdDecision.Status.SchedulingHistory[0]
	if len(entry.OrderedHosts) != 2 || entry.OrderedHosts[0] != "host-a" || entry.OrderedHosts[1] != "host-b" {
		t.Errorf("Expected ordered hosts ['host-a', 'host-b'], got %v", entry.OrderedHosts)
	}
	if entry.Intent != v1alpha1.SchedulingIntentUnknown {
		t.Errorf("Expected intent Unknown, got %v", entry.Intent)
	}
}

func TestBasePipelineController_UpdateDecision_EventEmission(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		orderedHosts      []string
		intent            v1alpha1.SchedulingIntent
		expectWarning     bool
		expectedEventType string
		expectedReason    string
		messageContains   string
	}{
		{
			name:              "successful scheduling emits normal event",
			orderedHosts:      []string{"host-a", "host-b"},
			intent:            v1alpha1.SchedulingIntentUnknown,
			expectWarning:     false,
			expectedEventType: corev1.EventTypeNormal,
			expectedReason:    "Unknown",
			messageContains:   "Scheduled to host-a",
		},
		{
			name:              "no valid hosts emits warning event",
			orderedHosts:      []string{},
			intent:            v1alpha1.SchedulingIntentUnknown,
			expectWarning:     true,
			expectedEventType: corev1.EventTypeWarning,
			expectedReason:    "NoValidHosts",
			messageContains:   "Cannot schedule: No valid hosts available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipelineConfig := &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain:  v1alpha1.SchedulingDomainNova,
					Type:              v1alpha1.PipelineTypeFilterWeigher,
					MaxHistoryEntries: 10,
				},
			}

			decision := &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-resource-id",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-resource-id",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(decision, pipelineConfig).
				WithStatusSubresource(decision).
				Build()

			// Create a mock event recorder
			recorder := &mockEventRecorder{
				events: make([]recordedEvent, 0),
			}

			controller := &BasePipelineController[any]{
				Client:           fakeClient,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				Recorder:         recorder,
				PipelineConfigs: map[string]v1alpha1.Pipeline{
					"test-pipeline": *pipelineConfig,
				},
			}

			update := DecisionUpdate{
				ResourceID:   "test-resource-id",
				PipelineName: "test-pipeline",
				Result: FilterWeigherPipelineResult{
					OrderedHosts: tt.orderedHosts,
				},
				Intent: tt.intent,
			}

			err := controller.updateDecision(context.Background(), update)
			if err != nil {
				t.Fatalf("updateDecision failed: %v", err)
			}

			// Verify event was emitted
			if len(recorder.events) != 1 {
				t.Fatalf("Expected 1 event, got %d", len(recorder.events))
			}

			event := recorder.events[0]

			// Check event type
			if event.eventType != tt.expectedEventType {
				t.Errorf("Expected event type %q, got %q", tt.expectedEventType, event.eventType)
			}

			// Check reason
			if event.reason != tt.expectedReason {
				t.Errorf("Expected reason %q, got %q", tt.expectedReason, event.reason)
			}

			// Check message contains expected text
			if !contains(event.message, tt.messageContains) {
				t.Errorf("Expected message to contain %q, got %q", tt.messageContains, event.message)
			}

			// Check action
			if event.action != "Scheduling" {
				t.Errorf("Expected action 'Scheduling', got %q", event.action)
			}
		})
	}
}

// mockEventRecorder implements events.EventRecorder for testing
type mockEventRecorder struct {
	events []recordedEvent
}

type recordedEvent struct {
	eventType string
	reason    string
	action    string
	message   string
}

func (m *mockEventRecorder) Eventf(object, related runtime.Object, eventType, reason, action, messageFmt string, args ...interface{}) {
	m.events = append(m.events, recordedEvent{
		eventType: eventType,
		reason:    reason,
		action:    action,
		message:   fmt.Sprintf(messageFmt, args...),
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

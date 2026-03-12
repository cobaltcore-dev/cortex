// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetName(t *testing.T) {
	tests := []struct {
		domain     v1alpha1.SchedulingDomain
		resourceID string
		expected   string
	}{
		{v1alpha1.SchedulingDomainNova, "uuid-1", "nova-uuid-1"},
		{v1alpha1.SchedulingDomainCinder, "vol-abc", "cinder-vol-abc"},
		{v1alpha1.SchedulingDomainManila, "share-xyz", "manila-share-xyz"},
		{v1alpha1.SchedulingDomainMachines, "machine-1", "machines-machine-1"},
		{v1alpha1.SchedulingDomainPods, "pod-ns-name", "pods-pod-ns-name"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := getName(tt.domain, tt.resourceID)
			if got != tt.expected {
				t.Errorf("getName(%q, %q) = %q, want %q", tt.domain, tt.resourceID, got, tt.expected)
			}
		})
	}
}

func TestGenerateExplanation(t *testing.T) {
	tests := []struct {
		name     string
		result   *v1alpha1.DecisionResult
		err      error
		expected string
	}{
		{
			name:     "nil result no error",
			result:   nil,
			err:      nil,
			expected: "",
		},
		{
			name:     "nil result with error",
			result:   nil,
			err:      errors.New("something broke"),
			expected: "Pipeline run failed: something broke.",
		},
		{
			name: "result with target host only no steps",
			result: &v1alpha1.DecisionResult{
				TargetHost: testlib.Ptr("host-1"),
			},
			expected: "Selected host: host-1.",
		},
		{
			name: "result with RawInWeights and filtering steps",
			result: &v1alpha1.DecisionResult{
				RawInWeights: map[string]float64{
					"host-a": 1.0,
					"host-b": 0.5,
					"host-c": 0.3,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "filter_capacity",
						Activations: map[string]float64{
							"host-a": 1.0,
							"host-b": 0.5,
						},
					},
					{
						StepName: "filter_status",
						Activations: map[string]float64{
							"host-a": 1.0,
						},
					},
				},
				TargetHost: testlib.Ptr("host-a"),
			},
			expected: "Started with 3 host(s).\n\n" +
				"filter_capacity filtered out host-c\n" +
				"filter_status filtered out host-b\n\n" +
				"1 hosts remaining (host-a)\n\n" +
				"Selected host: host-a.",
		},
		{
			name: "uses NormalizedInWeights when RawInWeights empty",
			result: &v1alpha1.DecisionResult{
				NormalizedInWeights: map[string]float64{
					"host-x": 0.6,
					"host-y": 0.4,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "weigher_cpu",
						Activations: map[string]float64{
							"host-x": 0.8,
							"host-y": 0.2,
						},
					},
				},
				TargetHost: testlib.Ptr("host-x"),
			},
			expected: "Started with 2 host(s).\n\n\n2 hosts remaining (host-x, host-y)\n\nSelected host: host-x.",
		},
		{
			name: "no weights no target",
			result: &v1alpha1.DecisionResult{
				StepResults: []v1alpha1.StepResult{
					{StepName: "some-step", Activations: map[string]float64{}},
				},
			},
			expected: "",
		},
		{
			name: "no weights with target",
			result: &v1alpha1.DecisionResult{
				TargetHost: testlib.Ptr("host-1"),
				StepResults: []v1alpha1.StepResult{
					{StepName: "some-step", Activations: map[string]float64{}},
				},
			},
			expected: "Selected host: host-1.",
		},
		{
			name: "all hosts survive all steps",
			result: &v1alpha1.DecisionResult{
				RawInWeights: map[string]float64{
					"host-a": 1.0,
					"host-b": 0.5,
				},
				StepResults: []v1alpha1.StepResult{
					{
						StepName: "noop",
						Activations: map[string]float64{
							"host-a": 1.0,
							"host-b": 0.5,
						},
					},
				},
				TargetHost: testlib.Ptr("host-a"),
			},
			expected: "Started with 2 host(s).\n\n\n2 hosts remaining (host-a, host-b)\n\nSelected host: host-a.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateExplanation(tt.result, tt.err)
			if got != tt.expected {
				t.Errorf("generateExplanation() =\n%q\nwant:\n%q", got, tt.expected)
			}
		})
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	return scheme
}

func TestHistoryManager_Upsert(t *testing.T) {
	tests := []struct {
		name string
		// setup returns a fake client and any pre-existing objects.
		setup func(t *testing.T) client.Client
		// decision to upsert.
		decision    *v1alpha1.Decision
		intent      v1alpha1.SchedulingIntent
		pipelineErr error
		// assertions on the history list (archived entries only).
		expectHistoryLen int
		// assertions on the current decision.
		expectTargetHost  *string
		expectSuccessful  bool
		expectCondStatus  metav1.ConditionStatus
		expectReason      string
		checkExplanation  func(t *testing.T, explanation string)
		checkCurrentHosts func(t *testing.T, hosts []string)
	}{
		{
			name: "create new history",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "uuid-1",
					PipelineRef:      corev1.ObjectReference{Name: "nova-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: testlib.Ptr("compute-1"),
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 0,
			expectTargetHost: testlib.Ptr("compute-1"),
			expectSuccessful: true,
			expectCondStatus: metav1.ConditionTrue,
			expectReason:     "SchedulingSucceeded",
			checkExplanation: func(t *testing.T, explanation string) {
				if !strings.Contains(explanation, "Selected host: compute-1") {
					t.Errorf("expected explanation to contain selected host, got: %q", explanation)
				}
			},
		},
		{
			name: "update existing history with pre-existing entries",
			setup: func(t *testing.T) client.Client {
				scheme := newTestScheme(t)
				existing := &v1alpha1.History{
					ObjectMeta: metav1.ObjectMeta{Name: "nova-uuid-2"},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						ResourceID:       "uuid-2",
					},
					Status: v1alpha1.HistoryStatus{
						History: []v1alpha1.SchedulingHistoryEntry{
							{Timestamp: metav1.Now(), PipelineRef: corev1.ObjectReference{Name: "old-pipeline"}},
						},
					},
				}
				return fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "uuid-2",
					PipelineRef:      corev1.ObjectReference{Name: "nova-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: testlib.Ptr("compute-2"),
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 1, // pre-existing entry preserved, no current to archive
			expectTargetHost: testlib.Ptr("compute-2"),
			expectSuccessful: true,
			expectCondStatus: metav1.ConditionTrue,
			expectReason:     "SchedulingSucceeded",
		},
		{
			name: "archive current to history on second upsert",
			setup: func(t *testing.T) client.Client {
				scheme := newTestScheme(t)
				existing := &v1alpha1.History{
					ObjectMeta: metav1.ObjectMeta{Name: "nova-uuid-3"},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						ResourceID:       "uuid-3",
					},
					Status: v1alpha1.HistoryStatus{
						Current: v1alpha1.CurrentDecision{
							Timestamp:   metav1.Now(),
							PipelineRef: corev1.ObjectReference{Name: "old-pipeline"},
							Intent:      v1alpha1.SchedulingIntentUnknown,
							Successful:  true,
							TargetHost:  testlib.Ptr("old-host"),
						},
					},
				}
				return fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "uuid-3",
					PipelineRef:      corev1.ObjectReference{Name: "new-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: testlib.Ptr("new-host"),
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 1, // old current archived
			expectTargetHost: testlib.Ptr("new-host"),
			expectSuccessful: true,
			expectCondStatus: metav1.ConditionTrue,
			expectReason:     "SchedulingSucceeded",
		},
		{
			name: "pipeline error",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					ResourceID:       "vol-1",
					PipelineRef:      corev1.ObjectReference{Name: "cinder-pipeline"},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			pipelineErr:      errors.New("no hosts available"),
			expectHistoryLen: 0,
			expectTargetHost: nil,
			expectSuccessful: false,
			expectCondStatus: metav1.ConditionFalse,
			expectReason:     "PipelineRunFailed",
			checkExplanation: func(t *testing.T, explanation string) {
				if !strings.Contains(explanation, "no hosts available") {
					t.Errorf("expected explanation to contain error text, got: %q", explanation)
				}
			},
		},
		{
			name: "no host found",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "uuid-nohost",
					PipelineRef:      corev1.ObjectReference{Name: "nova-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						// Pipeline succeeded but no target host selected (all filtered out).
						OrderedHosts: []string{},
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 0,
			expectTargetHost: nil,
			expectSuccessful: false,
			expectCondStatus: metav1.ConditionFalse,
			expectReason:     "NoHostFound",
		},
		{
			name: "history capped at 10 entries",
			setup: func(t *testing.T) client.Client {
				scheme := newTestScheme(t)
				entries := make([]v1alpha1.SchedulingHistoryEntry, 10)
				for i := range entries {
					entries[i] = v1alpha1.SchedulingHistoryEntry{
						Timestamp:   metav1.Now(),
						PipelineRef: corev1.ObjectReference{Name: fmt.Sprintf("pipeline-%d", i)},
					}
				}
				existing := &v1alpha1.History{
					ObjectMeta: metav1.ObjectMeta{Name: "manila-share-1"},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainManila,
						ResourceID:       "share-1",
					},
					Status: v1alpha1.HistoryStatus{
						Current: v1alpha1.CurrentDecision{
							Timestamp:   metav1.Now(),
							PipelineRef: corev1.ObjectReference{Name: "prev-pipeline"},
							Successful:  true,
							TargetHost:  testlib.Ptr("old-backend"),
						},
						History: entries,
					},
				}
				return fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainManila,
					ResourceID:       "share-1",
					PipelineRef:      corev1.ObjectReference{Name: "manila-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: testlib.Ptr("backend-1"),
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 10, // 10 existing + 1 archived current, capped to 10
			expectTargetHost: testlib.Ptr("backend-1"),
			expectSuccessful: true,
			expectCondStatus: metav1.ConditionTrue,
			expectReason:     "SchedulingSucceeded",
		},
		{
			name: "ordered hosts capped at 3",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithStatusSubresource(&v1alpha1.History{}).
					Build()
			},
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "uuid-cap",
					PipelineRef:      corev1.ObjectReference{Name: "nova-pipeline"},
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost:   testlib.Ptr("h1"),
						OrderedHosts: []string{"h1", "h2", "h3", "h4", "h5"},
					},
				},
			},
			intent:           v1alpha1.SchedulingIntentUnknown,
			expectHistoryLen: 0,
			expectTargetHost: testlib.Ptr("h1"),
			expectSuccessful: true,
			expectCondStatus: metav1.ConditionTrue,
			expectReason:     "SchedulingSucceeded",
			checkCurrentHosts: func(t *testing.T, hosts []string) {
				if len(hosts) != 3 {
					t.Errorf("ordered hosts length = %d, want 3", len(hosts))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup(t)
			hm := HistoryManager{Client: c}

			err := hm.Upsert(context.Background(), tt.decision, tt.intent, tt.pipelineErr)
			if err != nil {
				t.Fatalf("Upsert() returned error: %v", err)
			}

			// Fetch the history CRD.
			name := getName(tt.decision.Spec.SchedulingDomain, tt.decision.Spec.ResourceID)
			var history v1alpha1.History
			if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &history); err != nil {
				t.Fatalf("Failed to get history: %v", err)
			}

			// Verify spec.
			if history.Spec.SchedulingDomain != tt.decision.Spec.SchedulingDomain {
				t.Errorf("spec.schedulingDomain = %q, want %q", history.Spec.SchedulingDomain, tt.decision.Spec.SchedulingDomain)
			}
			if history.Spec.ResourceID != tt.decision.Spec.ResourceID {
				t.Errorf("spec.resourceID = %q, want %q", history.Spec.ResourceID, tt.decision.Spec.ResourceID)
			}

			// Verify history list length.
			if len(history.Status.History) != tt.expectHistoryLen {
				t.Errorf("status.history has %d entries, want %d", len(history.Status.History), tt.expectHistoryLen)
			}

			// Verify current decision.
			current := history.Status.Current
			if current.Successful != tt.expectSuccessful {
				t.Errorf("current.successful = %v, want %v", current.Successful, tt.expectSuccessful)
			}
			if current.PipelineRef.Name != tt.decision.Spec.PipelineRef.Name {
				t.Errorf("current.pipelineRef = %q, want %q", current.PipelineRef.Name, tt.decision.Spec.PipelineRef.Name)
			}

			// Verify target host.
			if tt.expectTargetHost == nil {
				if current.TargetHost != nil {
					t.Errorf("current.targetHost = %q, want nil", *current.TargetHost)
				}
			} else {
				if current.TargetHost == nil {
					t.Errorf("current.targetHost = nil, want %q", *tt.expectTargetHost)
				} else if *current.TargetHost != *tt.expectTargetHost {
					t.Errorf("current.targetHost = %q, want %q", *current.TargetHost, *tt.expectTargetHost)
				}
			}

			// Verify condition.
			if len(history.Status.Conditions) == 0 {
				t.Fatal("expected at least one condition")
			}
			readyCond := history.Status.Conditions[0]
			if readyCond.Status != tt.expectCondStatus {
				t.Errorf("condition status = %q, want %q", readyCond.Status, tt.expectCondStatus)
			}
			if readyCond.Reason != tt.expectReason {
				t.Errorf("condition reason = %q, want %q", readyCond.Reason, tt.expectReason)
			}

			// Verify explanation.
			if tt.checkExplanation != nil {
				tt.checkExplanation(t, current.Explanation)
			}

			// Verify ordered hosts on current.
			if tt.checkCurrentHosts != nil {
				tt.checkCurrentHosts(t, current.OrderedHosts)
			}
		})
	}
}

func TestHistoryManager_UpsertFromGoroutine(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&v1alpha1.History{}).
		Build()
	hm := HistoryManager{Client: c}

	decision := &v1alpha1.Decision{
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			ResourceID:       "async-uuid",
			PipelineRef:      corev1.ObjectReference{Name: "nova-pipeline"},
		},
		Status: v1alpha1.DecisionStatus{
			Result: &v1alpha1.DecisionResult{
				TargetHost: testlib.Ptr("compute-async"),
			},
		},
	}

	// Mirrors the pattern used in pipeline controllers.
	ctx := context.Background()
	go func() {
		if err := hm.Upsert(ctx, decision, v1alpha1.SchedulingIntentUnknown, nil); err != nil {
			t.Errorf("Upsert() returned error: %v", err)
		}
	}()

	// Poll for history creation.
	var histories v1alpha1.HistoryList
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := c.List(context.Background(), &histories); err != nil {
			t.Fatalf("Failed to list histories: %v", err)
		}
		if len(histories.Items) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for async history creation")
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := histories.Items[0].Status.Current.TargetHost
	if got == nil || *got != "compute-async" {
		t.Errorf("target host = %v, want %q", got, "compute-async")
	}
}

func TestHistoryManager_Delete(t *testing.T) {
	tests := []struct {
		name       string
		domain     v1alpha1.SchedulingDomain
		resourceID string
		preCreate  bool
	}{
		{
			name:       "delete existing",
			domain:     v1alpha1.SchedulingDomainNova,
			resourceID: "uuid-del",
			preCreate:  true,
		},
		{
			name:       "delete non-existing is no-op",
			domain:     v1alpha1.SchedulingDomainCinder,
			resourceID: "does-not-exist",
			preCreate:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			var objects []client.Object
			if tt.preCreate {
				objects = append(objects, &v1alpha1.History{
					ObjectMeta: metav1.ObjectMeta{
						Name: getName(tt.domain, tt.resourceID),
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: tt.domain,
						ResourceID:       tt.resourceID,
					},
				})
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			hm := HistoryManager{Client: c}

			err := hm.Delete(context.Background(), tt.domain, tt.resourceID)
			if err != nil {
				t.Fatalf("Delete() returned error: %v", err)
			}

			// Verify history is gone.
			var histories v1alpha1.HistoryList
			if err := c.List(context.Background(), &histories); err != nil {
				t.Fatalf("Failed to list histories: %v", err)
			}
			if len(histories.Items) != 0 {
				t.Errorf("expected 0 histories, got %d", len(histories.Items))
			}
		})
	}
}

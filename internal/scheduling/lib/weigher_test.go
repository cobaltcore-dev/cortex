// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockWeigher[RequestType FilterWeigherPipelineRequest] struct {
	InitFunc func(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error
	RunFunc  func(ctx context.Context, traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error)
}

func (m *mockWeigher[RequestType]) Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error {
	if m.InitFunc == nil {
		return nil
	}
	return m.InitFunc(ctx, client, step)
}
func (m *mockWeigher[RequestType]) Run(ctx context.Context, traceLog *slog.Logger, request RequestType) (*FilterWeigherPipelineStepResult, error) {
	if m.RunFunc == nil {
		return &FilterWeigherPipelineStepResult{}, nil
	}
	return m.RunFunc(ctx, traceLog, request)
}

// weigherTestOptions implements FilterWeigherPipelineStepOpts for testing.
type weigherTestOptions struct{}

func (o weigherTestOptions) Validate() error { return nil }

func TestBaseWeigher_Init(t *testing.T) {
	tests := []struct {
		name        string
		weigherSpec v1alpha1.WeigherSpec
		expectError bool
	}{
		{
			name: "successful initialization with valid params",
			weigherSpec: v1alpha1.WeigherSpec{
				Name: "test-weigher",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			expectError: false,
		},
		{
			name: "successful initialization with empty params",
			weigherSpec: v1alpha1.WeigherSpec{
				Name: "test-weigher",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			expectError: false,
		},
		{
			name: "error on invalid JSON params",
			weigherSpec: v1alpha1.WeigherSpec{
				Name: "test-weigher",
				Params: runtime.RawExtension{
					Raw: []byte(`{invalid json}`),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weigher := &BaseWeigher[mockFilterWeigherPipelineRequest, weigherTestOptions]{}
			cl := fake.NewClientBuilder().Build()

			err := weigher.Init(t.Context(), cl, tt.weigherSpec)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if !tt.expectError && weigher.Client == nil {
				t.Error("expected client to be set but it was nil")
			}
		})
	}
}

func TestBaseFilterWeigherPipelineStep_CheckKnowledges(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	tests := []struct {
		name        string
		knowledges  []v1alpha1.Knowledge
		refs        []corev1.ObjectReference
		expectError bool
		errorMsg    string
	}{
		{
			name: "all knowledges ready",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge1",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionReady,
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			refs: []corev1.ObjectReference{
				{Name: "knowledge1", Namespace: "default"},
			},
			expectError: false,
		},
		{
			name:       "knowledge not found",
			knowledges: []v1alpha1.Knowledge{},
			refs: []corev1.ObjectReference{
				{Name: "missing-knowledge", Namespace: "default"},
			},
			expectError: true,
			errorMsg:    "failed to get knowledge",
		},
		{
			name: "knowledge not ready - condition false",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge1",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionReady,
								Status: metav1.ConditionFalse,
							},
						},
					},
				},
			},
			refs: []corev1.ObjectReference{
				{Name: "knowledge1", Namespace: "default"},
			},
			expectError: true,
			errorMsg:    "not ready",
		},
		{
			name: "knowledge not ready - no data",
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge1",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 0,
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionReady,
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			refs: []corev1.ObjectReference{
				{Name: "knowledge1", Namespace: "default"},
			},
			expectError: true,
			errorMsg:    "no data available",
		},
		{
			name:        "empty knowledge list",
			knowledges:  []v1alpha1.Knowledge{},
			refs:        []corev1.ObjectReference{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.knowledges {
				clientBuilder = clientBuilder.WithObjects(&tt.knowledges[i])
			}
			cl := clientBuilder.Build()

			step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, weigherTestOptions]{
				Client: cl,
			}

			err := step.CheckKnowledges(t.Context(), tt.refs...)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && tt.errorMsg != "" {
				if !containsString(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			}
		})
	}
}

func TestBaseFilterWeigherPipelineStep_CheckKnowledges_NilClient(t *testing.T) {
	step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, weigherTestOptions]{
		Client: nil,
	}

	err := step.CheckKnowledges(t.Context(), corev1.ObjectReference{Name: "test", Namespace: "default"})

	if err == nil {
		t.Error("expected error for nil client but got nil")
	}
	if !containsString(err.Error(), "client not initialized") {
		t.Errorf("expected error message about client not initialized, got %q", err.Error())
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s != "" && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

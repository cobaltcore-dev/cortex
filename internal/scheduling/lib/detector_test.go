// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockDetection struct {
	resource string
	host     string
	reason   string
}

func (d mockDetection) GetResource() string                { return d.resource }
func (d mockDetection) GetHost() string                    { return d.host }
func (d mockDetection) GetReason() string                  { return d.reason }
func (d mockDetection) WithReason(reason string) Detection { d.reason = reason; return d }

type mockDetectorOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o mockDetectorOptions) Validate() error {
	return nil
}

func TestDetector_Init(t *testing.T) {
	step := BaseDetector[mockDetectorOptions]{}
	cl := fake.NewClientBuilder().Build()
	err := step.Init(t.Context(), cl, v1alpha1.DetectorSpec{
		Params: runtime.RawExtension{Raw: []byte(`{
			"option1": "value1",
			"option2": 2
		}`)},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if step.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", step.Options.Option1)
	}

	if step.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", step.Options.Option2)
	}
}

func TestDetector_Init_InvalidJSON(t *testing.T) {
	step := BaseDetector[mockDetectorOptions]{}
	cl := fake.NewClientBuilder().Build()
	err := step.Init(t.Context(), cl, v1alpha1.DetectorSpec{
		Params: runtime.RawExtension{Raw: []byte(`{invalid json}`)},
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBaseDetector_CheckKnowledges(t *testing.T) {
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
		{
			name: "multiple knowledges all ready",
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge2",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 5,
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
				{Name: "knowledge2", Namespace: "default"},
			},
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

			detector := &BaseDetector[mockDetectorOptions]{
				Client: cl,
			}

			err := detector.CheckKnowledges(t.Context(), tt.refs...)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && tt.errorMsg != "" {
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			}
		})
	}
}

func TestBaseDetector_CheckKnowledges_NilClient(t *testing.T) {
	detector := &BaseDetector[mockDetectorOptions]{
		Client: nil,
	}

	err := detector.CheckKnowledges(t.Context(), corev1.ObjectReference{Name: "test", Namespace: "default"})

	if err == nil {
		t.Error("expected error for nil client but got nil")
	}
	if !strings.Contains(err.Error(), "client not initialized") {
		t.Errorf("expected error message about client not initialized, got %q", err.Error())
	}
}

func TestBaseDetector_Validate(t *testing.T) {
	tests := []struct {
		name        string
		params      runtime.RawExtension
		expectError bool
	}{
		{
			name: "valid params",
			params: runtime.RawExtension{
				Raw: []byte(`{"option1": "value1", "option2": 2}`),
			},
			expectError: false,
		},
		{
			name: "empty params",
			params: runtime.RawExtension{
				Raw: []byte(`{}`),
			},
			expectError: false,
		},
		{
			name: "invalid JSON",
			params: runtime.RawExtension{
				Raw: []byte(`{invalid json}`),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := &BaseDetector[mockDetectorOptions]{}
			err := detector.Validate(t.Context(), tt.params)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

// failingDetectorOptions implements DetectionStepOpts and returns an error on Validate.
type failingDetectorOptions struct{}

func (o failingDetectorOptions) Validate() error {
	return errors.New("validation failed")
}

func TestBaseDetector_Validate_ValidationError(t *testing.T) {
	detector := &BaseDetector[failingDetectorOptions]{}
	err := detector.Validate(t.Context(), runtime.RawExtension{Raw: []byte(`{}`)})

	if err == nil {
		t.Error("expected error from validation but got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected error message to contain 'validation failed', got %q", err.Error())
	}
}

package filters

import (
	"fmt"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenericFilterStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        GenericFilterStepOpts
		expectError bool
	}{
		{
			name: "knowledge set",
			opts: GenericFilterStepOpts{
				Knowledge: "my-knowledge",
			},
			expectError: false,
		},
		{
			name: "knowledge unset",
			opts: GenericFilterStepOpts{
				Knowledge: "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestGenericFilterStep_Init(t *testing.T) {
	tests := []struct {
		name        string
		knowledge   v1alpha1.Knowledge
		expectError bool
	}{
		{
			name:        "knowledge not found",
			knowledge:   v1alpha1.Knowledge{},
			expectError: true,
		},
		{
			name: "knowledge found, but not ready",
			knowledge: v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 0,
				},
			},
			expectError: true,
		},
		{
			name: "knowledge found and ready",
			knowledge: v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 2,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, err := v1alpha1.SchemeBuilder.Build()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&tt.knowledge).Build()
			spec := v1alpha1.FilterSpec{
				Params: runtime.RawExtension{Raw: []byte(fmt.Sprintf(`{"knowledge": "%s"}`, tt.knowledge.Name))},
			}
			weigher := &GenericFilterStep{}
			err = weigher.Init(t.Context(), client, spec)
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestGenericFilterStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	features, err := v1alpha1.BoxFeatureList([]any{
		&plugins.Generic{Host: "node1", Value: 1.0},
		&plugins.Generic{Host: "node2", Value: 0.0},
	})
	if err != nil {
		t.Fatalf("failed to box features: %v", err)
	}

	knowledge := v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-knowledge",
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []metav1.Condition{
				{
					Type: v1alpha1.KnowledgeConditionReady,
				},
			},
			Raw:       features,
			RawLength: 2,
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&knowledge).Build()
	spec := v1alpha1.FilterSpec{
		Params: runtime.RawExtension{Raw: []byte(fmt.Sprintf(`{"knowledge": "%s"}`, knowledge.Name))},
	}
	weigher := &GenericFilterStep{}
	err = weigher.Init(t.Context(), client, spec)
	if err != nil {
		t.Fatalf("failed to initialize weigher: %v", err)
	}
	request := pods.PodPipelineRequest{
		Nodes: []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node2"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node3"},
			},
		},
		Pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
		},
	}

	actual, err := weigher.Run(t.Context(), nil, request)
	if err != nil {
		t.Fatalf("failed to run weigher: %v", err)
	}
	expected := map[string]float64{
		"node2": 0.0,
		"node3": 0.0,
	}
	for node, expectedWeight := range expected {
		if actual.Activations[node] != expectedWeight {
			t.Errorf("expected weight for node %s to be %f, got %f", node, expectedWeight, actual.Activations[node])
		}
	}
}

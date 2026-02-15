// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNoopFilter_Init(t *testing.T) {
	filter := &NoopFilter{}
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	err := filter.Init(t.Context(), cl, v1alpha1.FilterSpec{
		Name: "noop",
		Params: runtime.RawExtension{
			Raw: []byte(`{}`),
		},
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestNoopFilter_Run(t *testing.T) {
	tests := []struct {
		name     string
		request  pods.PodPipelineRequest
		expected map[string]float64
	}{
		{
			name: "empty nodes",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{},
				Pod:   corev1.Pod{},
			},
			expected: map[string]float64{},
		},
		{
			name: "single node",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					},
				},
				Pod: corev1.Pod{},
			},
			expected: map[string]float64{
				"node1": 0.0,
			},
		},
		{
			name: "multiple nodes",
			request: pods.PodPipelineRequest{
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
				Pod: corev1.Pod{},
			},
			expected: map[string]float64{
				"node1": 0.0,
				"node2": 0.0,
				"node3": 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &NoopFilter{}
			result, err := filter.Run(t.Context(), slog.Default(), tt.request)

			if err != nil {
				t.Errorf("expected Run() to succeed, got error: %v", err)
				return
			}

			if result == nil {
				t.Fatal("expected result to be non-nil")
				return
			}

			if len(result.Activations) != len(tt.expected) {
				t.Errorf("expected %d activations, got %d", len(tt.expected), len(result.Activations))
				return
			}

			for nodeName, expectedWeight := range tt.expected {
				actualWeight, ok := result.Activations[nodeName]
				if !ok {
					t.Errorf("expected activation for node %q, but not found", nodeName)
					continue
				}

				if actualWeight != expectedWeight {
					t.Errorf("expected weight for node %q to be %f, got %f", nodeName, expectedWeight, actualWeight)
				}
			}

			if result.Statistics == nil {
				t.Error("expected Statistics to be non-nil")
			}
		})
	}
}

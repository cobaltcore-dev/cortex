// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTaintFilter_Run(t *testing.T) {
	tests := []struct {
		name     string
		request  pods.PodPipelineRequest
		expected map[string]float64
	}{
		{
			name: "no taints, no tolerations",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					},
				},
				Pod: corev1.Pod{},
			},
			expected: map[string]float64{
				"node1": 0.0,
				"node2": 0.0,
			},
		},
		{
			name: "node with NoSchedule taint, pod without tolerations",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node-role.kubernetes.io/master",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					},
				},
				Pod: corev1.Pod{},
			},
			expected: map[string]float64{
				"node2": 0.0,
			},
		},
		{
			name: "node with NoSchedule taint, pod with matching toleration (Equal operator)",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node-role.kubernetes.io/master",
									Value:  "true",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Tolerations: []corev1.Toleration{
							{
								Key:      "node-role.kubernetes.io/master",
								Value:    "true",
								Operator: corev1.TolerationOpEqual,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 0.0,
			},
		},
		{
			name: "node with NoSchedule taint, pod with matching toleration (Exists operator)",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node-role.kubernetes.io/master",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Tolerations: []corev1.Toleration{
							{
								Key:      "node-role.kubernetes.io/master",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 0.0,
			},
		},
		{
			name: "node with NoSchedule taint, pod with non-matching toleration",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node-role.kubernetes.io/master",
									Value:  "true",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Tolerations: []corev1.Toleration{
							{
								Key:      "different-key",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			expected: map[string]float64{},
		},
		{
			name: "node with NoExecute taint (should not be filtered)",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node.kubernetes.io/not-ready",
									Effect: corev1.TaintEffectNoExecute,
								},
							},
						},
					},
				},
				Pod: corev1.Pod{},
			},
			expected: map[string]float64{
				"node1": 0.0,
			},
		},
		{
			name: "mixed nodes with different taints",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "node-role.kubernetes.io/master",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node3"},
						Spec: corev1.NodeSpec{
							Taints: []corev1.Taint{
								{
									Key:    "app",
									Value:  "database",
									Effect: corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Tolerations: []corev1.Toleration{
							{
								Key:      "app",
								Value:    "database",
								Operator: corev1.TolerationOpEqual,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node2": 0.0,
				"node3": 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &TaintFilter{}
			result, err := filter.Run(slog.Default(), tt.request)

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

func TestCanScheduleOnNode(t *testing.T) {
	tests := []struct {
		name     string
		node     corev1.Node
		pod      corev1.Pod
		expected bool
	}{
		{
			name:     "no taints",
			node:     corev1.Node{},
			pod:      corev1.Pod{},
			expected: true,
		},
		{
			name: "NoSchedule taint without matching toleration",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{
							Key:    "test-key",
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
			pod:      corev1.Pod{},
			expected: false,
		},
		{
			name: "NoSchedule taint with matching toleration",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{
							Key:    "test-key",
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "test-key",
							Operator: corev1.TolerationOpExists,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canScheduleOnNode(tt.node, tt.pod)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestHasToleration(t *testing.T) {
	tests := []struct {
		name     string
		pod      corev1.Pod
		taint    corev1.Taint
		expected bool
	}{
		{
			name: "no tolerations",
			pod:  corev1.Pod{},
			taint: corev1.Taint{
				Key:    "test-key",
				Effect: corev1.TaintEffectNoSchedule,
			},
			expected: false,
		},
		{
			name: "matching toleration with Exists operator",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "test-key",
							Operator: corev1.TolerationOpExists,
						},
					},
				},
			},
			taint: corev1.Taint{
				Key:    "test-key",
				Effect: corev1.TaintEffectNoSchedule,
			},
			expected: true,
		},
		{
			name: "matching toleration with Equal operator and matching value",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "test-key",
							Value:    "test-value",
							Operator: corev1.TolerationOpEqual,
						},
					},
				},
			},
			taint: corev1.Taint{
				Key:    "test-key",
				Value:  "test-value",
				Effect: corev1.TaintEffectNoSchedule,
			},
			expected: true,
		},
		{
			name: "non-matching toleration with Equal operator",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "test-key",
							Value:    "different-value",
							Operator: corev1.TolerationOpEqual,
						},
					},
				},
			},
			taint: corev1.Taint{
				Key:    "test-key",
				Value:  "test-value",
				Effect: corev1.TaintEffectNoSchedule,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasToleration(tt.pod, tt.taint)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

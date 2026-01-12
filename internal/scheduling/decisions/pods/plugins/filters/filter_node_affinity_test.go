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

func TestNodeAffinityFilter_Run(t *testing.T) {
	tests := []struct {
		name     string
		request  pods.PodPipelineRequest
		expected map[string]float64
	}{
		{
			name: "no node affinity",
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
				"node1": 1.0,
				"node2": 1.0,
			},
		},
		{
			name: "zone affinity with In operator",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "antarctica-east1",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "antarctica-west1",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node3",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "antarctica-north1",
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "topology.kubernetes.io/zone",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"antarctica-east1", "antarctica-west1"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 1.0,
				"node2": 1.0,
			},
		},
		{
			name: "node type affinity with NotIn operator",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"node.kubernetes.io/instance-type": "m5.large",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node2",
							Labels: map[string]string{
								"node.kubernetes.io/instance-type": "t3.micro",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node3"},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "node.kubernetes.io/instance-type",
													Operator: corev1.NodeSelectorOpNotIn,
													Values:   []string{"t3.micro"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 1.0,
				"node3": 1.0,
			},
		},
		{
			name: "exists operator",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"gpu": "true",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "gpu",
													Operator: corev1.NodeSelectorOpExists,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 1.0,
			},
		},
		{
			name: "does not exist operator",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"gpu": "true",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "gpu",
													Operator: corev1.NodeSelectorOpDoesNotExist,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node2": 1.0,
			},
		},
		{
			name: "multiple expressions in single term (AND logic)",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":      "antarctica-east1",
								"node.kubernetes.io/instance-type": "m5.large",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":      "antarctica-east1",
								"node.kubernetes.io/instance-type": "t3.micro",
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "topology.kubernetes.io/zone",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"antarctica-east1"},
												},
												{
													Key:      "node.kubernetes.io/instance-type",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"m5.large"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 1.0,
			},
		},
		{
			name: "multiple terms (OR logic)",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "antarctica-east1",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node2",
							Labels: map[string]string{
								"node.kubernetes.io/instance-type": "m5.large",
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node3"},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "topology.kubernetes.io/zone",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"antarctica-east1"},
												},
											},
										},
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "node.kubernetes.io/instance-type",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"m5.large"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 1.0,
				"node2": 1.0,
			},
		},
		{
			name: "no matching nodes",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "antarctica-north1",
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "topology.kubernetes.io/zone",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"antarctica-east1", "antarctica-west1"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &NodeAffinityFilter{}
			result, err := filter.Run(slog.Default(), tt.request)

			if err != nil {
				t.Errorf("expected Run() to succeed, got error: %v", err)
				return
			}

			if result == nil {
				t.Fatal("expected result to be non-nil")
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

func TestMatchesNodeAffinity(t *testing.T) {
	tests := []struct {
		name     string
		node     corev1.Node
		pod      corev1.Pod
		expected bool
	}{
		{
			name:     "no affinity",
			node:     corev1.Node{},
			pod:      corev1.Pod{},
			expected: true,
		},
		{
			name: "matching zone",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/zone": "antarctica-east1",
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "topology.kubernetes.io/zone",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"antarctica-east1", "antarctica-west1"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "non-matching zone",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/zone": "antarctica-north1",
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "topology.kubernetes.io/zone",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"antarctica-east1", "antarctica-west1"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesNodeAffinity(tt.node, tt.pod)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMatchesNodeSelectorRequirement(t *testing.T) {
	tests := []struct {
		name        string
		node        corev1.Node
		requirement corev1.NodeSelectorRequirement
		expected    bool
	}{
		{
			name: "In operator - matching value",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"zone": "east1",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "zone",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"east1", "west1"},
			},
			expected: true,
		},
		{
			name: "In operator - non-matching value",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"zone": "north1",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "zone",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"east1", "west1"},
			},
			expected: false,
		},
		{
			name: "In operator - missing label",
			node: corev1.Node{},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "zone",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"east1", "west1"},
			},
			expected: false,
		},
		{
			name: "NotIn operator - non-matching value",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"zone": "north1",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "zone",
				Operator: corev1.NodeSelectorOpNotIn,
				Values:   []string{"east1", "west1"},
			},
			expected: true,
		},
		{
			name: "NotIn operator - matching value",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"zone": "east1",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "zone",
				Operator: corev1.NodeSelectorOpNotIn,
				Values:   []string{"east1", "west1"},
			},
			expected: false,
		},
		{
			name: "Exists operator - label exists",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"gpu": "true",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "gpu",
				Operator: corev1.NodeSelectorOpExists,
			},
			expected: true,
		},
		{
			name: "Exists operator - label missing",
			node: corev1.Node{},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "gpu",
				Operator: corev1.NodeSelectorOpExists,
			},
			expected: false,
		},
		{
			name: "DoesNotExist operator - label missing",
			node: corev1.Node{},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "gpu",
				Operator: corev1.NodeSelectorOpDoesNotExist,
			},
			expected: true,
		},
		{
			name: "DoesNotExist operator - label exists",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"gpu": "true",
					},
				},
			},
			requirement: corev1.NodeSelectorRequirement{
				Key:      "gpu",
				Operator: corev1.NodeSelectorOpDoesNotExist,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesNodeSelectorRequirement(tt.node, tt.requirement)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

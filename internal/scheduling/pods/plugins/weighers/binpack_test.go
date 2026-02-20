// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"math"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBinpackingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        BinpackingStepOpts
		expectError bool
	}{
		{
			name: "valid options with positive weights",
			opts: BinpackingStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    2.0,
					corev1.ResourceMemory: 1.0,
				},
			},
			expectError: false,
		},
		{
			name: "valid options with zero weights",
			opts: BinpackingStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    0.0,
					corev1.ResourceMemory: 0.0,
				},
			},
			expectError: false,
		},
		{
			name: "valid options with empty weights",
			opts: BinpackingStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{},
			},
			expectError: false,
		},
		{
			name: "invalid options with negative weight",
			opts: BinpackingStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    -1.0,
					corev1.ResourceMemory: 1.0,
				},
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

func TestBinpackingStep_Run(t *testing.T) {
	tests := []struct {
		name     string
		step     *BinpackingStep
		request  pods.PodPipelineRequest
		expected map[string]float64
	}{
		{
			name: "node with different utilizations",
			step: &BinpackingStep{},
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "low-util-node"},
						Status: corev1.NodeStatus{
							Capacity: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4000m"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("3500m"),
								corev1.ResourceMemory: resource.MustParse("7Gi"),
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "high-util-node"},
						Status: corev1.NodeStatus{
							Capacity: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4000m"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("3Gi"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "test-container",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("500m"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"low-util-node":  0.25,      // (25%   CPU * 2 + 25% RAM * 1) / (2 + 1)
				"high-util-node": 2.0 / 3.0, // (62.5% CPU * 2 + 75% RAM * 1) / (2 + 1)
			},
		},
		{
			name: "node with custom resource",
			step: &BinpackingStep{},
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"},
						Status: corev1.NodeStatus{
							Capacity: corev1.ResourceList{
								corev1.ResourceCPU:                    resource.MustParse("8000m"),
								corev1.ResourceMemory:                 resource.MustParse("16Gi"),
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
							},
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                    resource.MustParse("1000m"),
								corev1.ResourceMemory:                 resource.MustParse("2Gi"),
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "gpu-workload",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:                    resource.MustParse("1000m"),
										corev1.ResourceMemory:                 resource.MustParse("2Gi"),
										corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"gpu-node": 0.75, // (100% CPU * 2 + 100% MEM * 1 + 50% GPU * 3) / (2 + 1 + 3)
			},
		},
		{
			name: "node with insufficient capacity",
			step: &BinpackingStep{},
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "small-node"},
						Status: corev1.NodeStatus{
							Capacity: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1500m"),
								corev1.ResourceMemory: resource.MustParse("3Gi"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "large-workload",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("3000m"), // Exceeds capacity
										corev1.ResourceMemory: resource.MustParse("2Gi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"small-node": 1.0, // Over-utilization gets clamped to 1.0
			},
		},
		{
			name: "node with missing capacity info",
			step: &BinpackingStep{},
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "incomplete-node"},
						Status:     corev1.NodeStatus{
							// Missing capacity/allocatable
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "test-container",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1000m"),
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"incomplete-node": 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.step.Options = BinpackingStepOpts{
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:                    2.0,
					corev1.ResourceMemory:                 1.0,
					corev1.ResourceName("nvidia.com/gpu"): 3.0,
				},
			}

			result, err := tt.step.Run(t.Context(), slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if result == nil {
				t.Fatal("expected result, got nil")
			}

			for nodeName, expectedScore := range tt.expected {
				actualScore, ok := result.Activations[nodeName]
				if !ok {
					t.Errorf("expected activation for node %s", nodeName)
					continue
				}

				if math.Abs(expectedScore-actualScore) > 1e-9 {
					t.Errorf("expected score %f for node %s, got %f", expectedScore, nodeName, actualScore)
				}
			}
		})
	}
}

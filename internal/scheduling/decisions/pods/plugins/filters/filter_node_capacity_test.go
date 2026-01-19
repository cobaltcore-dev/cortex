// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeCapacityFilter_Run(t *testing.T) {
	tests := []struct {
		name     string
		request  pods.PodPipelineRequest
		expected map[string]float64
	}{
		{
			name: "pod with no resource requests, nodes with capacity",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1000m"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "test-container",
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 0.0,
				"node2": 0.0,
			},
		},
		{
			name: "pod with resource requests, nodes with sufficient capacity",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1000m"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
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
										corev1.ResourceMemory: resource.MustParse("512Mi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node1": 0.0,
				"node2": 0.0,
			},
		},
		{
			name: "pod with resource requests, one node insufficient capacity",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"), // Insufficient CPU
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
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
										corev1.ResourceMemory: resource.MustParse("512Mi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node2": 0.0,
			},
		},
		{
			name: "pod with resource requests, no nodes have sufficient capacity",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"), // Insufficient memory
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"), // Insufficient CPU
								corev1.ResourceMemory: resource.MustParse("2Gi"),
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
										corev1.ResourceMemory: resource.MustParse("512Mi"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{},
		},
		{
			name: "pod with multiple containers",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                    resource.MustParse("2000m"),
								corev1.ResourceMemory:                 resource.MustParse("2Gi"),
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:                    resource.MustParse("500m"),
										corev1.ResourceMemory:                 resource.MustParse("512Mi"),
										corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
									},
								},
							},
							{
								Name: "container2",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("300m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
								},
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
			name: "pod with init containers",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2000m"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					},
				},
				Pod: corev1.Pod{
					Spec: corev1.PodSpec{
						InitContainers: []corev1.Container{
							{
								Name: "init1",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1000m"), // Largest init container
										corev1.ResourceMemory: resource.MustParse("1Gi"),
									},
								},
							},
							{
								Name: "init2",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("200m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name: "main-container",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("500m"),
										corev1.ResourceMemory: resource.MustParse("512Mi"),
									},
								},
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
			name: "node missing resource types",
			request: pods.PodPipelineRequest{
				Nodes: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node1"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("1000m"),
								// Missing memory resource and GPU resource
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "node2"},
						Status: corev1.NodeStatus{
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                    resource.MustParse("1000m"),
								corev1.ResourceMemory:                 resource.MustParse("1Gi"),
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
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
										corev1.ResourceMemory:                 resource.MustParse("512Mi"),
										corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
			expected: map[string]float64{
				"node2": 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &NodeCapacityFilter{}
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

func TestGetPodResourceRequests(t *testing.T) {
	tests := []struct {
		name     string
		pod      corev1.Pod
		expected corev1.ResourceList
	}{
		{
			name:     "pod with no containers",
			pod:      corev1.Pod{},
			expected: corev1.ResourceList{},
		},
		{
			name: "pod with single container",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
				},
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		{
			name: "pod with multiple containers",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("300m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
							},
						},
						{
							Name: "container2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("384Mi"),
			},
		},
		{
			name: "pod with init containers",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "init1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1000m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
						{
							Name: "init2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
				},
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"), // max(1000m init, 200m main)
				corev1.ResourceMemory: resource.MustParse("2Gi"),   // max(2Gi init, 512Mi main)
			},
		},
		{
			name: "pod with custom resources",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "gpu-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:                    resource.MustParse("2000m"),
									corev1.ResourceMemory:                 resource.MustParse("4Gi"),
									corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:                    resource.MustParse("2000m"),
				corev1.ResourceMemory:                 resource.MustParse("4Gi"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
			},
		},
		{
			name: "pod with mixed resources in multiple containers",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:                    resource.MustParse("1000m"),
									corev1.ResourceMemory:                 resource.MustParse("2Gi"),
									corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
								},
							},
						},
						{
							Name: "container2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:                     resource.MustParse("500m"),
									corev1.ResourceMemory:                  resource.MustParse("1Gi"),
									corev1.ResourceName("nvidia.com/gpu"):  resource.MustParse("1"),
									corev1.ResourceName("example.com/tpu"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:                     resource.MustParse("1500m"),
				corev1.ResourceMemory:                  resource.MustParse("3Gi"),
				corev1.ResourceName("nvidia.com/gpu"):  resource.MustParse("2"),
				corev1.ResourceName("example.com/tpu"): resource.MustParse("1"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPodResourceRequests(tt.pod)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d resource types, got %d", len(tt.expected), len(result))
				return
			}

			for resourceName, expectedQuantity := range tt.expected {
				actualQuantity, exists := result[resourceName]
				if !exists {
					t.Errorf("expected resource %q not found in result", resourceName)
					continue
				}

				if actualQuantity.Cmp(expectedQuantity) != 0 {
					t.Errorf("expected %s for resource %q, got %s", expectedQuantity.String(), resourceName, actualQuantity.String())
				}
			}
		})
	}
}

func TestHasCapacityForPod(t *testing.T) {
	tests := []struct {
		name        string
		node        corev1.Node
		podRequests corev1.ResourceList
		expected    bool
	}{
		{
			name: "node with sufficient capacity",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1000m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			expected: true,
		},
		{
			name: "node with insufficient CPU",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			expected: false,
		},
		{
			name: "node with insufficient memory",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1000m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			expected: false,
		},
		{
			name: "node missing requested resource type",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1000m"),
						// Missing memory
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			expected: false,
		},
		{
			name: "pod with no requests, node with minimum resources",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
				},
			},
			podRequests: corev1.ResourceList{},
			expected:    true,
		},
		{
			name: "node with sufficient custom resources",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:                    resource.MustParse("4000m"),
						corev1.ResourceMemory:                 resource.MustParse("8Gi"),
						corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:                    resource.MustParse("2000m"),
				corev1.ResourceMemory:                 resource.MustParse("4Gi"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
			expected: true,
		},
		{
			name: "node with insufficient custom resources",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:                    resource.MustParse("4000m"),
						corev1.ResourceMemory:                 resource.MustParse("8Gi"),
						corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:                    resource.MustParse("2000m"),
				corev1.ResourceMemory:                 resource.MustParse("4Gi"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
			},
			expected: false,
		},
		{
			name: "node missing custom resource type",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4000m"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
						// Missing nvidia.com/gpu
					},
				},
			},
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:                    resource.MustParse("2000m"),
				corev1.ResourceMemory:                 resource.MustParse("4Gi"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasCapacityForPod(tt.node, tt.podRequests)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

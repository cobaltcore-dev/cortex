// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package helpers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

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
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
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
			result := GetPodResourceRequests(&tt.pod)

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

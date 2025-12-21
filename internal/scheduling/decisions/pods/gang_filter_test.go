// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGangFilter_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}

	// Helper to create unstructured Pod with workloadRef
	createPodWithWorkload := func(name, ns, workloadName string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetUnstructuredContent(map[string]any{
			"spec": map[string]any{
				"workloadRef": map[string]any{
					"name": workloadName,
				},
			},
		})
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
		u.SetName(name)
		u.SetNamespace(ns)
		return u
	}

	// Helper to create unstructured Workload
	createWorkload := func(name, ns string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "scheduling.k8s.io", Version: "v1alpha1", Kind: "Workload"})
		u.SetName(name)
		u.SetNamespace(ns)
		return u
	}

	// Helper to create unstructured PodGroup
	createPodGroup := func(name, ns string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "scheduling.k8s.io", Version: "v1alpha1", Kind: "PodGroup"})
		u.SetName(name)
		u.SetNamespace(ns)
		return u
	}

	tests := []struct {
		name          string
		pod           *corev1.Pod
		existingObjs  []client.Object
		expectedNodes []string
		expectError   bool
	}{
		{
			name:          "pod is nil",
			pod:           nil,
			expectedNodes: nil,
			expectError:   true,
		},
		{
			name: "regular pod (no gang)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
			},
			existingObjs: []client.Object{
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
			},
			expectedNodes: []string{"node1", "node2"},
			expectError:   false,
		},
		{
			name: "pod with workloadRef (workload exists)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-workload", Namespace: "default"},
			},
			existingObjs: []client.Object{
				createPodWithWorkload("pod-workload", "default", "my-workload"),
				createWorkload("my-workload", "default"),
			},
			expectedNodes: []string{"node1", "node2"},
			expectError:   false,
		},
		{
			name: "pod with workloadRef (workload missing)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-workload-missing", Namespace: "default"},
			},
			existingObjs: []client.Object{
				createPodWithWorkload("pod-workload-missing", "default", "missing-workload"),
			},
			expectedNodes: []string{}, // All filtered out
			expectError:   false,
		},
		{
			name: "pod with gang label (group exists)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-gang",
					Namespace: "default",
					Labels: map[string]string{
						"pod-group.scheduling.k8s.io/name": "my-gang",
					},
				},
			},
			existingObjs: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-gang",
						Namespace: "default",
						Labels: map[string]string{
							"pod-group.scheduling.k8s.io/name": "my-gang",
						},
					},
				},
				createPodGroup("my-gang", "default"),
			},
			expectedNodes: []string{"node1", "node2"},
			expectError:   false,
		},
		{
			name: "pod with gang label (group missing)",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-gang-missing",
					Namespace: "default",
					Labels: map[string]string{
						"pod-group.scheduling.k8s.io/name": "missing-gang",
					},
				},
			},
			existingObjs: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-gang-missing",
						Namespace: "default",
						Labels: map[string]string{
							"pod-group.scheduling.k8s.io/name": "missing-gang",
						},
					},
				},
			},
			expectedNodes: []string{}, // All filtered out
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existingObjs...).
				Build()

			filter := &GangFilter{}
			// Initialize the filter with the fake client
			err := filter.Init(context.Background(), fakeClient, v1alpha1.Step{})
			if err != nil {
				t.Fatalf("failed to init filter: %v", err)
			}

			nodes := []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node1"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node2"}},
			}

			req := pods.PodPipelineRequest{
				Nodes: nodes,
				Pod:   tt.pod,
			}

			result, err := filter.Run(slog.Default(), req)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check activations
			if len(result.Activations) != len(tt.expectedNodes) {
				t.Errorf("expected %d activations, got %d", len(tt.expectedNodes), len(result.Activations))
			}
			for _, node := range tt.expectedNodes {
				if score, ok := result.Activations[node]; !ok || score != 1.0 {
					t.Errorf("node %s expected 1.0, got %v", node, score)
				}
			}
		})
	}
}

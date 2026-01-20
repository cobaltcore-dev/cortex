// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/podgroupsets"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodGroupSetPipeline_Run(t *testing.T) {
	node1 := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1000m"),
			},
		},
	}

	pgs := v1alpha1.PodGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: v1alpha1.PodGroupSetSpec{
			PodGroups: []v1alpha1.PodGroup{
				{
					Name: "group1",
					Spec: v1alpha1.PodGroupSpec{
						Replicas: 2,
						PodSpec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("400m"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	pipeline := &PodGroupSetPipeline{}
	request := podgroupsets.PodGroupSetPipelineRequest{
		PodGroupSet: pgs,
		Nodes:       []corev1.Node{node1},
	}

	result, err := pipeline.Run(request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(result.TargetPlacements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.TargetPlacements))
	}

	if result.TargetPlacements["group1-0"] != "node1" {
		t.Errorf("expected group1-0 on node1, got %s", result.TargetPlacements["group1-0"])
	}

	// Test failure case
	pgsFail := pgs.DeepCopy()
	pgsFail.Spec.PodGroups[0].Spec.Replicas = 3 // 3 * 400m = 1200m > 1000m

	requestFail := podgroupsets.PodGroupSetPipelineRequest{
		PodGroupSet: *pgsFail,
		Nodes:       []corev1.Node{node1},
	}

	_, err = pipeline.Run(requestFail)
	if err == nil {
		t.Fatal("expected error, got none")
	}
}

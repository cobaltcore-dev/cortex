// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/decisions/pods/helpers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NodeCapacityFilter struct {
	Alias string
}

func (f *NodeCapacityFilter) Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error {
	return nil
}

func (NodeCapacityFilter) Run(traceLog *slog.Logger, request pods.PodPipelineRequest) (*lib.StepResult, error) {
	activations := make(map[string]float64)
	stats := make(map[string]lib.StepStatistics)

	podRequests := helpers.GetPodResourceRequests(request.Pod)

	for _, node := range request.Nodes {
		if hasCapacityForPod(node, podRequests) {
			activations[node.Name] = 0.0
		}
	}

	return &lib.StepResult{Activations: activations, Statistics: stats}, nil
}

func hasCapacityForPod(node corev1.Node, podRequests corev1.ResourceList) bool {
	for resourceName, requestedQuantity := range podRequests {
		allocatableQuantity, exists := node.Status.Allocatable[resourceName]
		if !exists {
			return false
		}

		if requestedQuantity.Cmp(allocatableQuantity) > 0 {
			return false
		}
	}

	return true
}

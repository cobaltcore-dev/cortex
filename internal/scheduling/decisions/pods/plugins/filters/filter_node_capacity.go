// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
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

	podRequests := getPodResourceRequests(request.Pod)

	for _, node := range request.Nodes {
		if hasCapacityForPod(node, podRequests) {
			activations[node.Name] = 0.0
		}
	}

	return &lib.StepResult{Activations: activations, Statistics: stats}, nil
}

func getPodResourceRequests(pod corev1.Pod) corev1.ResourceList {
	requests := make(corev1.ResourceList)

	for _, container := range pod.Spec.Containers {
		addResourcesInto(requests, container.Resources.Requests)
	}

	// Init containers run sequentially to other containers,
	// thus the maximum of all requests is determined
	initRequests := make(corev1.ResourceList)
	for _, initContainer := range pod.Spec.InitContainers {
		maxResourcesInto(initRequests, initContainer.Resources.Requests)
	}
	maxResourcesInto(requests, initRequests)

	return requests
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

func addResourcesInto(dst, src corev1.ResourceList) {
	for resource, qty := range src {
		if existing, ok := dst[resource]; ok {
			qty.Add(existing)
		}
		dst[resource] = qty
	}
}

func maxResourcesInto(dst, src corev1.ResourceList) {
	for resource, qty := range src {
		if existing, ok := dst[resource]; !ok || qty.Cmp(existing) > 0 {
			dst[resource] = qty
		}
	}
}

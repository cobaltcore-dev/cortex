// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NodeAvailableFilter struct {
	Alias string
}

func (f *NodeAvailableFilter) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return nil
}

func (NodeAvailableFilter) Run(_ context.Context, traceLog *slog.Logger, request pods.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	activations := make(map[string]float64)
	stats := make(map[string]lib.FilterWeigherPipelineStepStatistics)

	for _, node := range request.Nodes {
		if isNodeHealthy(node) && isNodeSchedulable(node) {
			activations[node.Name] = 0.0
		}
	}

	return &lib.FilterWeigherPipelineStepResult{Activations: activations, Statistics: stats}, nil
}

func isNodeHealthy(node corev1.Node) bool {
	isUnhealthyConditions := map[corev1.NodeConditionType]bool{
		corev1.NodeMemoryPressure:     true,
		corev1.NodeDiskPressure:       true,
		corev1.NodePIDPressure:        true,
		corev1.NodeNetworkUnavailable: true,
	}

	isReady := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				isReady = true
			} else {
				return false
			}
		}

		if _, exists := isUnhealthyConditions[condition.Type]; exists {
			if condition.Status == corev1.ConditionTrue {
				return false
			}
		}
	}

	return isReady
}

func isNodeSchedulable(node corev1.Node) bool {
	return !node.Spec.Unschedulable
}

func init() {
	Index["nodeavailable"] = func() PodFilter { return &NodeAvailableFilter{} }
}

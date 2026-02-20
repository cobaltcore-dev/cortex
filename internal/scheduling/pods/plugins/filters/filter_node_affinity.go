// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NodeAffinityFilter struct {
	Alias string
}

func (f *NodeAffinityFilter) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return nil
}

func (NodeAffinityFilter) Run(_ context.Context, traceLog *slog.Logger, request pods.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	activations := make(map[string]float64)
	stats := make(map[string]lib.FilterWeigherPipelineStepStatistics)

	for _, node := range request.Nodes {
		if matchesNodeAffinity(node, request.Pod) {
			activations[node.Name] = 0.0
		}
	}

	return &lib.FilterWeigherPipelineStepResult{Activations: activations, Statistics: stats}, nil
}

func matchesNodeAffinity(node corev1.Node, pod corev1.Pod) bool {
	if pod.Spec.Affinity == nil || pod.Spec.Affinity.NodeAffinity == nil {
		return true
	}

	required := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if required == nil {
		return true
	}

	for _, term := range required.NodeSelectorTerms {
		if matchesNodeSelectorTerm(node, term) {
			return true
		}
	}

	return false
}

func matchesNodeSelectorTerm(node corev1.Node, term corev1.NodeSelectorTerm) bool {
	for _, expr := range term.MatchExpressions {
		if !matchesNodeSelectorRequirement(node, expr) {
			return false
		}
	}
	return true
}

func matchesNodeSelectorRequirement(node corev1.Node, req corev1.NodeSelectorRequirement) bool {
	nodeValue, exists := node.Labels[req.Key]

	switch req.Operator {
	case corev1.NodeSelectorOpIn:
		if !exists {
			return false
		}
		for _, value := range req.Values {
			if nodeValue == value {
				return true
			}
		}
		return false
	case corev1.NodeSelectorOpNotIn:
		if !exists {
			return true
		}
		for _, value := range req.Values {
			if nodeValue == value {
				return false
			}
		}
		return true
	case corev1.NodeSelectorOpExists:
		return exists
	case corev1.NodeSelectorOpDoesNotExist:
		return !exists
	case corev1.NodeSelectorOpGt:
		if !exists || len(req.Values) == 0 {
			return false
		}
		nodeInt, err1 := strconv.Atoi(nodeValue)
		reqInt, err2 := strconv.Atoi(req.Values[0])
		if err1 != nil || err2 != nil {
			return false
		}
		return nodeInt > reqInt
	case corev1.NodeSelectorOpLt:
		if !exists || len(req.Values) == 0 {
			return false
		}
		nodeInt, err1 := strconv.Atoi(nodeValue)
		reqInt, err2 := strconv.Atoi(req.Values[0])
		if err1 != nil || err2 != nil {
			return false
		}
		return nodeInt < reqInt
	default:
		return false
	}
}

func init() {
	Index["nodeaffinity"] = func() PodFilter { return &NodeAffinityFilter{} }
}

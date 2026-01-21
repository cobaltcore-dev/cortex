// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
)

type PodPipelineRequest struct {
	// The available nodes.
	Nodes []corev1.Node `json:"nodes"`
	// The pod to be scheduled.
	Pod corev1.Pod `json:"pod"`
}

func (r PodPipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Nodes))
	for i, host := range r.Nodes {
		hosts[i] = host.Name
	}
	return hosts
}
func (r PodPipelineRequest) GetWeights() map[string]float64 {
	weights := make(map[string]float64, len(r.Nodes))
	for _, node := range r.Nodes {
		weights[node.Name] = 0.0
	}
	return weights
}
func (r PodPipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{}
}
func (r PodPipelineRequest) FilterSubjects(includedSubjects map[string]float64) lib.PipelineRequest {
	filteredNodes := make([]corev1.Node, 0, len(includedSubjects))
	for _, node := range r.Nodes {
		if _, exists := includedSubjects[node.Name]; exists {
			filteredNodes = append(filteredNodes, node)
		}
	}
	r.Nodes = filteredNodes
	return r
}

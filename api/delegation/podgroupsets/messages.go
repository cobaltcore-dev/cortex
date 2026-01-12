// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type PodGroupSetPipelineRequest struct {
	PodGroupSet v1alpha1.PodGroupSet `json:"podGroupSet"`
	Nodes       []corev1.Node        `json:"nodes"`
}

func (r PodGroupSetPipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Nodes))
	for i, host := range r.Nodes {
		hosts[i] = host.Name
	}
	return hosts
}

func (r PodGroupSetPipelineRequest) GetWeights() map[string]float64 {
	weights := make(map[string]float64, len(r.Nodes))
	for _, node := range r.Nodes {
		weights[node.Name] = 0.0
	}
	return weights
}

func (r PodGroupSetPipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{
		slog.String("podGroupSet", r.PodGroupSet.Name),
		slog.String("namespace", r.PodGroupSet.Namespace),
	}
}

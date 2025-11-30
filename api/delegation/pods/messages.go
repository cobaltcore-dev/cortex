// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"log/slog"

	podsv1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/pods/v1alpha1"
)

type PodPipelineRequest struct {
	// The available nodes.
	Nodes []podsv1alpha1.Node `json:"nodes"`
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

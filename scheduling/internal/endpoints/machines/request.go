// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/lib/scheduling"
	"github.com/cobaltcore-dev/cortex/machines/api/v1alpha1"
)

type MachinePipelineRequest struct {
	// The available machine pools.
	Pools []v1alpha1.MachinePool `json:"pools"`

	// The name of the pipeline to execute.
	// By default the required pipeline with the name "default" will be used.
	Pipeline string `json:"pipeline"`
}

func (r MachinePipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Pools))
	for i, host := range r.Pools {
		hosts[i] = host.Name
	}
	return hosts
}
func (r MachinePipelineRequest) GetWeights() map[string]float64 {
	weights := make(map[string]float64, len(r.Pools))
	for _, pool := range r.Pools {
		weights[pool.Name] = 0.0
	}
	return weights
}
func (r MachinePipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{}
}
func (r MachinePipelineRequest) GetPipeline() string {
	return r.Pipeline
}
func (r MachinePipelineRequest) WithPipeline(pipeline string) scheduling.PipelineRequest {
	r.Pipeline = pipeline
	return r
}

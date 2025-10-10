// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
)

type MachinePipelineRequest struct {
	// The available machine pools.
	Pools []computev1alpha1.MachinePool `json:"pools"`

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
	return make(map[string]float64, len(r.Pools))
}
func (r MachinePipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{}
}
func (r MachinePipelineRequest) GetPipeline() string {
	return r.Pipeline
}
func (r MachinePipelineRequest) WithPipeline(pipeline string) lib.PipelineRequest {
	r.Pipeline = pipeline
	return r
}

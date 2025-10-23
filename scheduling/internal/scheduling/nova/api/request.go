// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

// Conform to the PipelineRequest interface.

type PipelineRequest api.ExternalSchedulerRequest

func (r PipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Hosts))
	for i, host := range r.Hosts {
		hosts[i] = host.ComputeHost
	}
	return hosts
}
func (r PipelineRequest) GetWeights() map[string]float64 {
	return r.Weights
}
func (r PipelineRequest) GetTraceLogArgs() []slog.Attr {
	greq := ""
	if r.Context.GlobalRequestID != nil {
		greq = *r.Context.GlobalRequestID
	}
	return []slog.Attr{
		slog.String("greq", greq),
		slog.String("req", r.Context.RequestID),
		slog.String("user", r.Context.UserID),
		slog.String("project", r.Context.ProjectID),
	}
}
func (r PipelineRequest) GetPipeline() string {
	return r.Pipeline
}
func (r PipelineRequest) WithPipeline(pipeline string) scheduling.PipelineRequest {
	r.Pipeline = pipeline
	return r
}

package api

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/scheduler/external/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
)

// Conform to the PipelineRequest interface.

type ExternalSchedulerRequest cinder.ExternalSchedulerRequest

func (r ExternalSchedulerRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Hosts))
	for i, host := range r.Hosts {
		hosts[i] = host.VolumeHost
	}
	return hosts
}
func (r ExternalSchedulerRequest) GetWeights() map[string]float64 {
	return r.Weights
}
func (r ExternalSchedulerRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{
		slog.String("greq", r.Context.GlobalRequestID),
		slog.String("req", r.Context.RequestID),
		slog.String("user", r.Context.UserID),
		slog.String("project", r.Context.ProjectID),
	}
}
func (r ExternalSchedulerRequest) IsSandboxed() bool {
	return r.Sandboxed
}
func (r ExternalSchedulerRequest) WithSandboxed(sandboxed bool) scheduler.PipelineRequest {
	r.Sandboxed = sandboxed
	return r
}

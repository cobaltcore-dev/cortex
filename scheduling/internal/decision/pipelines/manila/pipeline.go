// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/manila/api"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/manila/plugins/netapp"
)

type PipelineRequest api.ExternalSchedulerRequest

func (r PipelineRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Hosts))
	for i, host := range r.Hosts {
		hosts[i] = host.ShareHost
	}
	return hosts
}
func (r PipelineRequest) GetWeights() map[string]float64 {
	return r.Weights
}
func (r PipelineRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{
		slog.String("greq", r.Context.GlobalRequestID),
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

type ManilaStep = scheduling.Step[api.PipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() ManilaStep{
	(&netapp.CPUUsageBalancingStep{}).GetName(): func() ManilaStep { return &netapp.CPUUsageBalancingStep{} },
}

// Create a new Manila scheduler pipeline.
func NewPipeline(
	config conf.ManilaSchedulerPipelineConfig,
	db db.DB,
	monitor scheduling.PipelineMonitor,
	mqttClient mqtt.Client,
) scheduling.Pipeline[api.PipelineRequest] {

	// Wrappers to apply to each step in the pipeline.
	wrappers := []scheduling.StepWrapper[api.PipelineRequest, struct{}]{
		// Validate that no hosts are removed.
		func(s ManilaStep, c conf.ManilaSchedulerStepConfig) ManilaStep {
			return scheduling.ValidateStep(s, c.DisabledValidations)
		},
		// Monitor the step execution.
		func(s ManilaStep, c conf.ManilaSchedulerStepConfig) ManilaStep {
			return scheduling.MonitorStep(s, monitor)
		},
	}
	return scheduling.NewPipeline(supportedSteps, config.Plugins, wrappers, db, monitor)
}

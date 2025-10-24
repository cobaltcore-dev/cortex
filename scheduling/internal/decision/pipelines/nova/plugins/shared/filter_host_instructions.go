// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

type FilterHostInstructionsStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterHostInstructionsStep) GetName() string { return "filter_host_instructions" }

// Filter hosts based on instructions given in the request spec. Supported are:
// - spec.ignore_hosts: Filter out all hosts in this list.
// - spec.force_hosts: Include only hosts in this list.
func (s *FilterHostInstructionsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	if request.Spec.Data.IgnoreHosts != nil {
		for _, host := range *request.Spec.Data.IgnoreHosts {
			delete(result.Activations, host)
			traceLog.Debug("filtering host which is ignored", "host", host)
		}
	}
	if request.Spec.Data.ForceHosts != nil {
		for host := range result.Activations {
			if !slices.Contains(*request.Spec.Data.ForceHosts, host) {
				delete(result.Activations, host)
				traceLog.Debug("filtering host which is not forced", "host", host)
			}
		}
	}
	return result, nil
}

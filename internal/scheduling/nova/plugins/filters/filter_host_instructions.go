// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterHostInstructionsStep struct {
	lib.BaseFilter[compute.SchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Filter hosts based on instructions given in the request spec. Supported are:
// - spec.ignore_hosts: Filter out all hosts in this list.
// - spec.force_hosts: Include only hosts in this list.
func (s *FilterHostInstructionsStep) Run(traceLog logr.Logger, request compute.SchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if len(request.IgnoreHosts) > 0 {
		for _, host := range request.IgnoreHosts {
			delete(result.Activations, host)
			traceLog.V(1).Info("filtering host which is ignored", "host", host)
		}
	} else {
		traceLog.V(1).Info("no hosts to ignore specified")
	}
	if len(request.ForceHosts) > 0 {
		for host := range result.Activations {
			if !slices.Contains(request.ForceHosts, host) {
				delete(result.Activations, host)
				traceLog.V(1).Info("filtering host which is not forced", "host", host)
			}
		}
	} else {
		traceLog.V(1).Info("no forced hosts specified")
	}
	return result, nil
}

func init() {
	Index["filter_host_instructions"] = func() NovaFilter { return &FilterHostInstructionsStep{} }
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Step that can be used to filter out specific compute host names.
// This step is useful to circumvent scheduling issues on specific hosts by
// excluding them from all scheduling decisions.
type FilterExcludeHostsStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterExcludeHostsStepOpts]
}

type FilterExcludeHostsStepOpts struct {
	// Hosts to exclude from scheduling. This can be used to exclude hosts that
	// are known to be unhealthy, for example.
	ExcludedHosts []string `json:"excludedHosts"`
}

// No validation for now, but we could add checks to ensure that the excluded
// hosts are valid if needed.
func (opts FilterExcludeHostsStepOpts) Validate() error { return nil }

func (s *FilterExcludeHostsStep) Run(
	_ context.Context,
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)
	for _, host := range s.Options.ExcludedHosts {
		delete(result.Activations, host) // noop if host is not in the map
		traceLog.Info("filtering out host based on excluded hosts configuration", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_exclude_hosts"] = func() NovaFilter { return &FilterExcludeHostsStep{} }
}

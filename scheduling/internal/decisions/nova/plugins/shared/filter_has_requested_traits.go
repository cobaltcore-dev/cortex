// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

type FilterHasRequestedTraits struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Filter hosts that do not have the requested traits given by the extra spec:
// - "trait:<trait>": "forbidden" means the host must not have the specified trait.
// - "trait:<trait>": "required" means the host must have the specified trait.
func (s *FilterHasRequestedTraits) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	var requiredTraits, forbiddenTraits []string
	for key, value := range request.Spec.Data.Flavor.Data.ExtraSpecs {
		if !strings.HasPrefix(key, "trait:") {
			continue
		}
		traitName := strings.TrimPrefix(key, "trait:")
		switch value {
		case "forbidden":
			forbiddenTraits = append(forbiddenTraits, traitName)
		case "required":
			requiredTraits = append(requiredTraits, traitName)
		}
	}
	if len(requiredTraits) == 0 && len(forbiddenTraits) == 0 {
		traceLog.Debug("no traits requested, skipping filter")
		return result, nil
	}
	var hostCapabilities []shared.HostCapabilities
	if _, err := s.DB.SelectTimed(
		"scheduler-nova", &hostCapabilities, "SELECT * FROM "+shared.HostCapabilities{}.TableName(),
	); err != nil {
		return result, err
	}
	hostsEncountered := map[string]struct{}{}
	for _, cap := range hostCapabilities {
		hostsEncountered[cap.ComputeHost] = struct{}{}
		providedTraits := cap.Traits // Comma-separated list.
		allRequiredPresent := true
		for _, required := range requiredTraits {
			if !strings.Contains(providedTraits, required) {
				allRequiredPresent = false
				break
			}
		}
		allForbiddenAbsent := true
		for _, forbidden := range forbiddenTraits {
			if strings.Contains(providedTraits, forbidden) {
				allForbiddenAbsent = false
				break
			}
		}
		if !allRequiredPresent || !allForbiddenAbsent {
			delete(result.Activations, cap.ComputeHost)
			traceLog.Debug(
				"filtering host which does not match trait check",
				"host", cap.ComputeHost, "want", requiredTraits,
				"forbid", forbiddenTraits, "have", providedTraits,
			)
		}
	}
	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.Debug(
				"removing host with unknown capabilities",
				"host", host,
			)
		}
	}
	return result, nil
}
